// Package youtube is a thin HTTP wrapper around the YouTube Data API v3.
//
// It exposes only the endpoints YuHaul needs for discovery: subscriptions.list,
// channels.list, playlistItems.list, and videos.list. Every paged or batched
// API call can be reported to a CostRecorder so quota consumption is tracked
// outside the client.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"
)

// Operation names the categories of API calls used for quota accounting.
type Operation string

// Known YouTube Data API v3 operations used by this wrapper.
const (
	OpSubscriptionsList   Operation = "subscriptions_list"
	OpChannelsList        Operation = "channels_list"
	OpPlaylistItemsList   Operation = "playlist_items_list"
	OpVideosList          Operation = "videos_list"
	OpPlaylistItemsInsert Operation = "playlist_items_insert"
	OpPlaylistsList       Operation = "playlists_list"
)

// CostRecorder is invoked after each API call with the operation type and the
// number of calls represented by that request (one for most methods; one per
// batch for videos.list and channels.list).
type CostRecorder func(ctx context.Context, op Operation, calls int) error

// ReadAttempt describes one admitted Data API read, not a planned batch.
type ReadAttempt struct {
	Operation  Operation
	AdmittedAt time.Time
	Status     int
	Category   string
}

type ReadRecorder func(context.Context, ReadAttempt) error

// ReadError keeps the primary read error separate from accounting and renewal.
// Its text never includes the latter errors, which may contain sensitive data.
type ReadError struct {
	Primary      error
	Accounting   error
	Renewal      error
	Interruption error
	Status       int
}

// ReadTransportError marks a failed admitted or refused transport operation
// without retaining request URLs or untrusted transport error text.
type ReadTransportError struct{ Canceled bool }

func (e *ReadTransportError) Error() string { return "youtube read transport failed" }

func (e *ReadError) Error() string {
	if e.Primary != nil {
		return e.Primary.Error() + " (read processing failed)"
	}
	return "youtube read accounting failed"
}
func (e *ReadError) Unwrap() error { return e.Primary }

var providerReasons = map[string]struct{}{
	"quotaExceeded": {}, "dailyLimitExceeded": {}, "rateLimitExceeded": {}, "userRateLimitExceeded": {},
	"authError": {}, "forbidden": {}, "insufficientPermissions": {}, "backendError": {}, "internalError": {},
	"badRequest": {}, "invalidParameter": {}, "notFound": {}, "playlistNotFound": {}, "videoNotFound": {},
	"playlistItemsNotAccessible": {}, "playlistContainsMaximumNumberOfVideos": {}, "videoAlreadyInPlaylist": {},
}

func registeredProviderReason(reason string) bool { _, ok := providerReasons[reason]; return ok }

// APIError describes an HTTP error returned by the YouTube API. It carries
// only the status and a vetted provider reason; the raw response body is
// never retained so it cannot reach storage, API projections, events, or
// logs through err.Error().
type APIError struct {
	Status   int
	Reason   string
	Reasons  []string
	Complete bool
}

// InvalidResponseError marks a successful HTTP response whose body could not
// be decoded into the expected YouTube response shape. Its rendered message is
// fixed; the parse error remains available only through Unwrap for control
// flow and typed inspection.
type InvalidResponseError struct {
	cause     error
	operation Operation
}

func (e *InvalidResponseError) Error() string { return "invalid YouTube API response" }
func (e *InvalidResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *InvalidResponseError) Operation() Operation {
	if e == nil {
		return ""
	}
	return e.operation
}

func invalidResponseError(cause error, operation Operation) error {
	return &InvalidResponseError{cause: cause, operation: operation}
}

func (e *APIError) Error() string {
	if e == nil {
		return "youtube API error"
	}
	if e.Status < 300 || e.Status > 599 {
		return "youtube API error"
	}
	if !registeredProviderReason(e.Reason) {
		return fmt.Sprintf("youtube API returned %d", e.Status)
	}
	return fmt.Sprintf("youtube API returned %d: %s", e.Status, e.Reason)
}

// IsQuotaExceeded reports whether err represents YouTube's quotaExceeded reason.
func IsQuotaExceeded(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Reason == "quotaExceeded"
}

// Client makes authenticated YouTube Data API v3 calls.
type Client struct {
	baseURL           string
	httpClient        *http.Client
	recorder          CostRecorder
	logger            *slog.Logger
	observeRejected   APIReadRejectionObserver
	requestDispatcher RequestDispatcher
	readRecorder      ReadRecorder
	readPreflight     func(context.Context, Operation) error
	quotaReads        bool
	transportErr      error
	singleAttempt     http.RoundTripper
}

// RequestAdmission identifies the authorization revision used by one read request.
type RequestAdmission struct {
	AuthorizationID    string
	CredentialRevision int64
	AdmittedAt         time.Time
	Finalize           func(context.Context, string, int) error
	Accounting         error
}

// RejectionObserver lets a caller fence a rejected credential for subsequent requests.
type APIReadRejectionObserver func(context.Context, RequestAdmission) error

// RequestDispatcher admits a request and returns its exact credential receipt.
type RequestDispatcher func(context.Context, *http.Request, http.RoundTripper) (*http.Response, RequestAdmission, error)

type rejectionObservationKey struct{}
type rejectionObservation struct {
	err       error
	admission RequestAdmission
	operation Operation
	observe   APIReadRejectionObserver
}

// ReadOperation returns the operation associated with an authorized read.
func ReadOperation(ctx context.Context) (Operation, bool) {
	state, ok := ctx.Value(rejectionObservationKey{}).(*rejectionObservation)
	if !ok || state.operation == "" {
		return "", false
	}
	return state.operation, true
}

// WithReadAccounting installs per-attempt preflight and accounting for reads.
func WithReadAccounting(preflight func(context.Context, Operation) error, recorder ReadRecorder) Option {
	return func(c *Client) { c.readPreflight, c.readRecorder = preflight, recorder }
}

// WithQuotaReads enables read error handling for dispatchers that return a finalizer.
func WithQuotaReads() Option { return func(c *Client) { c.quotaReads = true } }

// WithRejectionObserver observes 401 responses for the exact credential that made a request.
func WithRejectionObserver(observer APIReadRejectionObserver) Option {
	return func(c *Client) { c.observeRejected = observer }
}

// WithRequestDispatcher installs an authority admission fence for requests.
func WithRequestDispatcher(dispatch RequestDispatcher) Option {
	return func(c *Client) { c.requestDispatcher = dispatch }
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the default production YouTube Data API base URL. It is
// used by tests and by the YUHAUL_YOUTUBE_BASE_URL runtime seam so Product
// Acceptance can point the assembled binary at a fake YouTube endpoint.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(u, "/")
	}
}

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithSingleAttemptTransport supplies a controlled transport whose owner guarantees
// that RoundTrip issues at most one request and does not follow redirects. It is
// intended for controlled test transports, not arbitrary middleware wrappers.
func WithSingleAttemptTransport(transport http.RoundTripper) Option {
	return func(c *Client) { c.singleAttempt = transport }
}

// WithRecorder sets the cost recorder invoked after each API call.
func WithRecorder(r CostRecorder) Option {
	return func(c *Client) {
		c.recorder = r
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		c.logger = l
	}
}

// NewClient constructs a Client using token for authentication. The returned
// client uses a fresh *http.Client so no connection state leaks between runs.
func NewClient(ctx context.Context, token *oauth2.Token, opts ...Option) *Client {
	c := &Client{
		baseURL:    "https://www.googleapis.com/youtube/v3",
		httpClient: oauth2.NewClient(ctx, oauth2.StaticTokenSource(token)),
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewAuthorizedClient constructs a client whose authorization is resolved for
// each outgoing request. The resolver should be bound to one invocation's
// authorization identity (for example, Service.Resolve with a Bind result).
func NewAuthorizedClient(ctx context.Context, resolve func(context.Context) (*oauth2.Token, error), opts ...Option) *Client {
	baseClient, _ := ctx.Value(oauth2.HTTPClient).(*http.Client)
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	httpClient := *baseClient
	c := &Client{baseURL: "https://www.googleapis.com/youtube/v3", httpClient: &httpClient, logger: slog.Default()}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
		c.transportErr = errors.New("youtube: nil HTTP client")
	}
	// Never modify the caller's client, which may also be used for OAuth.
	clientCopy := *c.httpClient
	c.httpClient = &clientCopy
	base := c.httpClient.Transport
	if base == nil {
		base = baseClient.Transport
	}
	if base == nil {
		base = http.DefaultTransport
	}
	if c.singleAttempt != nil {
		base = c.singleAttempt
	} else if transport, ok := base.(*http.Transport); ok && transport != nil && transport.DialTLS == nil && transport.DialTLSContext == nil {
		clone := transport.Clone()
		clone.DisableKeepAlives = true
		// Discard alternate protocol handlers, including any installed after
		// the source transport's first use. Clone drops registered schemes.
		clone.TLSNextProto = nil
		if clone.TLSClientConfig != nil {
			clone.TLSClientConfig = clone.TLSClientConfig.Clone()
			clone.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
		clone.Protocols = new(http.Protocols)
		clone.Protocols.SetHTTP1(true)
		base = clone
	} else {
		c.transportErr = errors.New("youtube: unsupported transport for single-attempt dispatch")
	}
	c.httpClient.Transport = authorizationTransport{resolve: resolve, base: base, dispatch: c.requestDispatcher, observe: c.observeRejected, transportErr: c.transportErr}
	c.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

type authorizationTransport struct {
	resolve      func(context.Context) (*oauth2.Token, error)
	base         http.RoundTripper
	dispatch     RequestDispatcher
	observe      APIReadRejectionObserver
	transportErr error
}

func (t authorizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.transportErr != nil {
		return nil, t.transportErr
	}
	if t.dispatch != nil {
		resp, admission, err := t.dispatch(req.Context(), req, t.base)
		if state, ok := req.Context().Value(rejectionObservationKey{}).(*rejectionObservation); ok {
			state.admission = admission
			state.observe = t.observe
		} else if resp != nil && admission.AuthorizationID != "" && t.observe != nil {
			rejected := resp.StatusCode == http.StatusUnauthorized
			if resp.StatusCode == http.StatusForbidden {
				body, readErr := io.ReadAll(resp.Body)
				if readErr == nil {
					resp.Body.Close()
					resp.Body = io.NopCloser(bytes.NewReader(body))
					api := youtubeAPIError(resp.StatusCode, body).(*APIError)
					rejected = api.Complete && len(api.Reasons) == 1 && api.Reasons[0] == "authError"
				}
			}
			if rejected {
				_ = t.observe(req.Context(), admission)
			}
		}
		return resp, err
	}
	if t.resolve == nil {
		return nil, errors.New("youtube: authorization resolver is unavailable")
	}
	token, err := t.resolve(req.Context())
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	token.SetAuthHeader(clone)
	return t.base.RoundTrip(clone)
}

// Subscription represents a channel the authenticated user is subscribed to.
type Subscription struct {
	ChannelID string
}

// SubscriptionList holds a page of subscriptions and pagination state.
type SubscriptionList struct {
	Items         []Subscription
	NextPageToken string
	PageCount     int // number of API calls consumed (one per page)
}

// ListSubscriptions retrieves the authenticated user's subscriptions one page
// at a time. Pass nextPageToken from the previous response to continue paging.
func (c *Client) ListSubscriptions(ctx context.Context, nextPageToken string) (*SubscriptionList, error) {
	q := url.Values{}
	q.Set("part", "snippet")
	q.Set("mine", "true")
	q.Set("maxResults", "50")
	if nextPageToken != "" {
		q.Set("pageToken", nextPageToken)
	}

	body, err := c.get(ctx, "/subscriptions", q, OpSubscriptionsList, 1)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}

	var raw struct {
		Items         []subscriptionItem `json:"items"`
		NextPageToken string             `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode subscriptions: %w", invalidResponseError(err, OpSubscriptionsList))
	}

	out := &SubscriptionList{
		NextPageToken: raw.NextPageToken,
		PageCount:     1,
	}
	for _, it := range raw.Items {
		if it.Snippet.ResourceID.ChannelID == "" {
			continue
		}
		out.Items = append(out.Items, Subscription{ChannelID: it.Snippet.ResourceID.ChannelID})
	}
	return out, nil
}

type subscriptionItem struct {
	Snippet struct {
		ResourceID struct {
			ChannelID string `json:"channelId"`
		} `json:"resourceId"`
	} `json:"snippet"`
}

// Playlist represents a YouTube playlist owned by the authenticated user.
type Playlist struct {
	ID    string
	Title string
}

// ListPlaylists retrieves the authenticated user's playlists one page at a
// time. Pass nextPageToken from the previous response to continue paging.
func (c *Client) ListPlaylists(ctx context.Context, nextPageToken string) ([]Playlist, string, error) {
	q := url.Values{}
	q.Set("part", "snippet")
	q.Set("mine", "true")
	q.Set("maxResults", "50")
	if nextPageToken != "" {
		q.Set("pageToken", nextPageToken)
	}

	body, err := c.get(ctx, "/playlists", q, OpPlaylistsList, 1)
	if err != nil {
		return nil, "", fmt.Errorf("list playlists: %w", err)
	}

	var raw struct {
		Items         []playlistListItem `json:"items"`
		NextPageToken string             `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", fmt.Errorf("decode playlists: %w", invalidResponseError(err, OpPlaylistsList))
	}

	out := make([]Playlist, 0, len(raw.Items))
	for _, it := range raw.Items {
		if it.ID == "" {
			continue
		}
		out = append(out, Playlist{
			ID:    it.ID,
			Title: it.Snippet.Title,
		})
	}
	return out, raw.NextPageToken, nil
}

type playlistListItem struct {
	ID      string  `json:"id"`
	Snippet snippet `json:"snippet"`
}

// Channel represents metadata about a YouTube channel, including the cached
// uploads playlist identifier.
type Channel struct {
	ID                string
	Title             string
	UploadsPlaylistID string
}

// GetChannels fetches channel metadata for up to 50 channel IDs in a single
// API call. The YouTube API accepts a comma-separated id list.
func (c *Client) GetChannels(ctx context.Context, channelIDs []string) ([]Channel, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	if len(channelIDs) > 50 {
		return nil, fmt.Errorf("get channels: too many ids %d (max 50)", len(channelIDs))
	}

	q := url.Values{}
	q.Set("part", "contentDetails,snippet")
	q.Set("id", strings.Join(channelIDs, ","))
	q.Set("maxResults", strconv.Itoa(len(channelIDs)))

	body, err := c.get(ctx, "/channels", q, OpChannelsList, 1)
	if err != nil {
		return nil, fmt.Errorf("get channels: %w", err)
	}

	var raw struct {
		Items []channelItem `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode channels: %w", invalidResponseError(err, OpChannelsList))
	}

	out := make([]Channel, 0, len(raw.Items))
	for _, it := range raw.Items {
		if it.ID == "" {
			continue
		}
		out = append(out, Channel{
			ID:                it.ID,
			Title:             it.Snippet.Title,
			UploadsPlaylistID: it.ContentDetails.RelatedPlaylists.Uploads,
		})
	}
	return out, nil
}

type channelItem struct {
	ID             string  `json:"id"`
	Snippet        snippet `json:"snippet"`
	ContentDetails struct {
		RelatedPlaylists struct {
			Uploads string `json:"uploads"`
		} `json:"relatedPlaylists"`
	} `json:"contentDetails"`
}

// PlaylistItem represents a video returned from a channel's uploads playlist.
type PlaylistItem struct {
	VideoID      string
	Title        string
	ChannelID    string
	PublishedAt  time.Time
	ThumbnailURL string
}

// ListPlaylistItems retrieves videos from a channel's uploads playlist. It
// returns the items, the next page token, and any error. Only items whose
// publishedAt time is after watermark are included when watermark is non-zero.
func (c *Client) ListPlaylistItems(ctx context.Context, playlistID, nextPageToken string) ([]PlaylistItem, string, error) {
	if playlistID == "" {
		return nil, "", fmt.Errorf("list playlist items: playlistID is required")
	}

	q := url.Values{}
	q.Set("part", "snippet")
	q.Set("playlistId", playlistID)
	q.Set("maxResults", "50")
	if nextPageToken != "" {
		q.Set("pageToken", nextPageToken)
	}

	body, err := c.get(ctx, "/playlistItems", q, OpPlaylistItemsList, 1)
	if err != nil {
		return nil, "", fmt.Errorf("list playlist items %s: %w", playlistID, err)
	}

	var raw struct {
		Items         []playlistItem `json:"items"`
		NextPageToken string         `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", fmt.Errorf("decode playlist items: %w", invalidResponseError(err, OpPlaylistItemsList))
	}

	out := make([]PlaylistItem, 0, len(raw.Items))
	for _, it := range raw.Items {
		if it.Snippet.ResourceID.VideoID == "" {
			continue
		}
		pub, err := parseTime(it.Snippet.PublishedAt)
		if err != nil {
			c.logger.DebugContext(ctx, "skipping playlist item with invalid publishedAt")
			continue
		}
		out = append(out, PlaylistItem{
			VideoID:      it.Snippet.ResourceID.VideoID,
			Title:        it.Snippet.Title,
			ChannelID:    it.Snippet.ChannelID,
			PublishedAt:  pub,
			ThumbnailURL: bestThumbnail(it.Snippet.Thumbnails),
		})
	}
	return out, raw.NextPageToken, nil
}

type playlistItem struct {
	Snippet struct {
		Title       string `json:"title"`
		ChannelID   string `json:"channelId"`
		PublishedAt string `json:"publishedAt"`
		ResourceID  struct {
			VideoID string `json:"videoId"`
		} `json:"resourceId"`
		Thumbnails thumbnails `json:"thumbnails"`
	} `json:"snippet"`
}

// VideoDetail is the metadata returned by videos.list.
type VideoDetail struct {
	ID              string
	Title           string
	ChannelID       string
	ChannelTitle    string
	Description     string
	DurationSeconds int
	PublishedAt     time.Time
	ThumbnailURL    string
	// Present identifies valid route inputs in GetVideosWithReturnedIDs.
	// GetVideos does not populate it.
	Present VideoDetailPresence
}

// VideoDetailPresence distinguishes valid empty or zero values from missing or invalid inputs.
type VideoDetailPresence struct {
	Title, ChannelID, ChannelTitle, Description, Duration bool
}

// VideoIDs reports the identifiable requested IDs of a validated by-ID
// response. An element with an explicit empty-string id names no video and is
// skipped, which leaves Complete false; every other identity defect makes the
// whole response invalid.
type VideoIDs struct {
	IDs      []string
	Complete bool
	// Live has one entry for every ID in IDs (BR-042, live-status ARS-001 §2).
	Live map[string]LiveStatus
}

// LiveStatus is the parser's classification of one returned video.
type LiveStatus string

const (
	LiveNone        LiveStatus = "none"
	LiveNow         LiveStatus = "live"
	LiveUpcoming    LiveStatus = "upcoming"
	LiveUnconfirmed LiveStatus = "unconfirmed"
)

// classifyLive applies the first matching rule of live-status ARS-001 §2. It
// never reads the duration, and keeps no raw value.
func classifyLive(item map[string]json.RawMessage, duplicateItem map[string]bool) LiveStatus {
	if duplicateItem["snippet"] {
		return LiveUnconfirmed
	}
	snippet, snippetDups, ok := readJSONObject(item["snippet"])
	if !ok || len(snippetDups) > 0 {
		return LiveUnconfirmed
	}
	lbc, ok := jsonString(snippet["liveBroadcastContent"])
	if !ok {
		return LiveUnconfirmed
	}
	switch lbc {
	case "live":
		return LiveNow
	case "upcoming":
		return LiveUpcoming
	case "none":
	default:
		return LiveUnconfirmed
	}
	rawDetails, has := item["liveStreamingDetails"]
	if !has && !duplicateItem["liveStreamingDetails"] {
		return LiveNone
	}
	if duplicateItem["liveStreamingDetails"] {
		return LiveUnconfirmed
	}
	details, detailDups, ok := readJSONObject(rawDetails)
	if !ok || len(detailDups) > 0 {
		return LiveUnconfirmed
	}
	endRaw, hasEnd := jsonString(details["actualEndTime"])
	if !hasEnd {
		return LiveUnconfirmed
	}
	end, err := parseTime(endRaw)
	if err != nil {
		return LiveUnconfirmed
	}
	if _, present := details["activeLiveChatId"]; present {
		return LiveUnconfirmed
	}
	if _, present := details["concurrentViewers"]; present {
		return LiveUnconfirmed
	}
	if rawStart, present := details["actualStartTime"]; present {
		startRaw, ok := jsonString(rawStart)
		if !ok {
			return LiveUnconfirmed
		}
		start, err := parseTime(startRaw)
		if err != nil || start.After(end) {
			return LiveUnconfirmed
		}
	}
	return LiveNone
}

// GetVideosWithReturnedIDs fetches at most 50 distinct IDs and validates the
// complete response before allowing callers to infer an omitted ID.
//
// An invalid envelope, or an element whose identity is duplicate, unrequested,
// malformed or ambiguous, is an invalid response: nothing is returned. An
// element in a valid envelope whose ID is the empty string names no video. It
// is skipped, the other elements keep their own attribution, and Complete is
// false, so a caller may use the identified peers but must not infer any
// omission (ARS-005 §6.1). Callers that cannot use partial results must treat
// Complete=false as unusable.
func (c *Client) GetVideosWithReturnedIDs(ctx context.Context, videoIDs []string) ([]VideoDetail, VideoIDs, error) {
	transport, guarded := c.httpClient.Transport.(authorizationTransport)
	if (c.readRecorder == nil && !c.quotaReads) || !guarded || transport.dispatch == nil {
		return nil, VideoIDs{}, &ReadError{Accounting: errors.New("youtube read admission or accounting unavailable")}
	}
	requested := make(map[string]bool, len(videoIDs))
	for _, id := range videoIDs {
		if !validVideoID(id) || requested[id] {
			return nil, VideoIDs{}, errors.New("get videos: invalid requested IDs")
		}
		requested[id] = true
	}
	if len(videoIDs) == 0 || len(videoIDs) > 50 {
		return nil, VideoIDs{}, errors.New("get videos: expected 1 to 50 distinct IDs")
	}
	q := url.Values{}
	q.Set("part", "snippet,contentDetails,liveStreamingDetails")
	q.Set("id", strings.Join(videoIDs, ","))
	q.Set("maxResults", strconv.Itoa(len(videoIDs)))
	body, err := c.getByID(ctx, "/videos", q, OpVideosList, 1, true)
	if err != nil {
		return nil, VideoIDs{}, fmt.Errorf("get videos: %w", err)
	}
	invalid := func() ([]VideoDetail, VideoIDs, error) {
		return nil, VideoIDs{}, invalidResponseError(nil, OpVideosList)
	}
	envelope, duplicates, ok := readJSONObject(body)
	if !ok || duplicates["items"] || duplicates["nextPageToken"] {
		return invalid()
	}
	if token, ok := envelope["nextPageToken"]; ok {
		var s string
		if string(token) == "null" || json.Unmarshal(token, &s) != nil || s != "" {
			return invalid()
		}
	}
	items, ok := envelope["items"]
	if !ok || len(items) == 0 || items[0] != '[' {
		return invalid()
	}
	var rawItems []json.RawMessage
	if json.Unmarshal(items, &rawItems) != nil {
		return invalid()
	}
	result := VideoIDs{IDs: make([]string, 0, len(rawItems)), Live: make(map[string]LiveStatus, len(rawItems))}
	details := make([]VideoDetail, 0, len(rawItems))
	seen := make(map[string]bool, len(rawItems))
	// anonymous is true when an element of a valid envelope names no ID at all
	// (an empty string). It attributes nothing, so it cannot make another
	// element ambiguous, but it may be the one a requested ID is missing from:
	// the set of returned IDs is then not complete and proves no omission
	// (ARS-005 §6.1, D7).
	anonymous := false
	for _, raw := range rawItems {
		item, duplicateItem, ok := readJSONObject(raw)
		if !ok || duplicateItem["id"] {
			return invalid()
		}
		id, valid := jsonString(item["id"])
		if valid && id == "" {
			anonymous = true
			continue
		}
		if !valid || !validVideoID(id) || !requested[id] || seen[id] {
			return invalid()
		}
		seen[id] = true
		result.IDs = append(result.IDs, id)
		result.Live[id] = classifyLive(item, duplicateItem)
		// Ambiguous detail fields are unavailable, but a reliable ID remains returned.
		var snippet, content map[string]json.RawMessage
		if !duplicateItem["snippet"] {
			snippet = readJSONObjectWithoutDuplicates(item["snippet"])
		}
		if !duplicateItem["contentDetails"] {
			content = readJSONObjectWithoutDuplicates(item["contentDetails"])
		}
		var d VideoDetail
		d.ID = id
		d.Title, d.Present.Title = jsonString(snippet["title"])
		d.ChannelID, d.Present.ChannelID = jsonString(snippet["channelId"])
		d.ChannelTitle, d.Present.ChannelTitle = jsonString(snippet["channelTitle"])
		d.Description, d.Present.Description = jsonString(snippet["description"])
		duration, hasDuration := jsonString(content["duration"])
		if hasDuration && duration != "" {
			d.DurationSeconds, d.Present.Duration = validDuration(duration)
		}
		published, present := jsonString(snippet["publishedAt"])
		if !present {
			continue
		}
		d.PublishedAt, err = parseTime(published)
		if err != nil {
			continue
		}
		var thumbs thumbnails
		_ = json.Unmarshal(snippet["thumbnails"], &thumbs)
		d.ThumbnailURL = bestThumbnail(thumbs)
		details = append(details, d)
	}
	result.Complete = !anonymous
	return details, result, nil
}

var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var byIDDurationRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// readJSONObject preserves duplicate-member evidence which map decoding loses.
func readJSONObject(raw []byte) (map[string]json.RawMessage, map[string]bool, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, false
	}
	values := make(map[string]json.RawMessage)
	duplicates := make(map[string]bool)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		name, ok := key.(string)
		if !ok {
			return nil, nil, false
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil, nil, false
		}
		if _, exists := values[name]; exists {
			duplicates[name] = true
		}
		values[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, false
	}
	var extra json.RawMessage
	if dec.Decode(&extra) != io.EOF {
		return nil, nil, false
	}
	return values, duplicates, true
}

func readJSONObjectWithoutDuplicates(raw []byte) map[string]json.RawMessage {
	values, duplicates, _ := readJSONObject(raw)
	for name := range duplicates {
		delete(values, name)
	}
	return values
}

func validVideoID(id string) bool { return videoIDRe.MatchString(id) }

func jsonString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || !utf8.Valid(raw) || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func validDuration(s string) (int, bool) {
	m := byIDDurationRe.FindStringSubmatch(s)
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "" && m[4] == "") || strings.HasSuffix(s, "T") {
		return 0, false
	}
	max := int(^uint(0) >> 1)
	total := 0
	for i, factor := range []int{86400, 3600, 60, 1} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n > (max-total)/factor {
			return 0, false
		}
		total += n * factor
	}
	return total, true
}

// GetVideos fetches full video metadata for up to 50 video IDs per API call.
func (c *Client) GetVideos(ctx context.Context, videoIDs []string) ([]VideoDetail, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}
	if len(videoIDs) > 50 {
		return nil, fmt.Errorf("get videos: too many ids %d (max 50)", len(videoIDs))
	}

	q := url.Values{}
	q.Set("part", "snippet,contentDetails")
	q.Set("id", strings.Join(videoIDs, ","))
	q.Set("maxResults", strconv.Itoa(len(videoIDs)))

	body, err := c.get(ctx, "/videos", q, OpVideosList, 1)
	if err != nil {
		return nil, fmt.Errorf("get videos: %w", err)
	}

	var raw struct {
		Items []videoItem `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode videos: %w", invalidResponseError(err, OpVideosList))
	}

	out := make([]VideoDetail, 0, len(raw.Items))
	for _, it := range raw.Items {
		pub, err := parseTime(it.Snippet.PublishedAt)
		if err != nil {
			c.logger.DebugContext(ctx, "skipping video with invalid publishedAt")
			continue
		}
		dur, _ := parseDuration(it.ContentDetails.Duration)
		out = append(out, VideoDetail{
			ID:              it.ID,
			Title:           it.Snippet.Title,
			ChannelID:       it.Snippet.ChannelID,
			ChannelTitle:    it.Snippet.ChannelTitle,
			Description:     it.Snippet.Description,
			DurationSeconds: dur,
			PublishedAt:     pub,
			ThumbnailURL:    bestThumbnail(it.Snippet.Thumbnails),
		})
	}
	return out, nil
}

type videoItem struct {
	ID             string  `json:"id"`
	Snippet        snippet `json:"snippet"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
}

type snippet struct {
	Title        string     `json:"title"`
	ChannelID    string     `json:"channelId"`
	ChannelTitle string     `json:"channelTitle"`
	Description  string     `json:"description"`
	PublishedAt  string     `json:"publishedAt"`
	Thumbnails   thumbnails `json:"thumbnails"`
}

type thumbnails struct {
	Default thumb `json:"default"`
	Medium  thumb `json:"medium"`
	High    thumb `json:"high"`
}

type thumb struct {
	URL string `json:"url"`
}

func bestThumbnail(t thumbnails) string {
	if t.High.URL != "" {
		return t.High.URL
	}
	if t.Medium.URL != "" {
		return t.Medium.URL
	}
	return t.Default.URL
}

// InsertPlaylistItem adds videoID to playlistID. It returns an error if the
// API call fails.
func (c *Client) InsertPlaylistItem(ctx context.Context, playlistID, videoID string) error {
	body := map[string]any{
		"snippet": map[string]any{
			"playlistId": playlistID,
			"resourceId": map[string]string{
				"kind":    "youtube#video",
				"videoId": videoID,
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal insert body: %w", err)
	}

	q := url.Values{"part": {"snippet"}}
	if _, err := c.post(ctx, "/playlistItems", q, OpPlaylistItemsInsert, payload); err != nil {
		return fmt.Errorf("insert playlist item: %w", err)
	}
	return nil
}
func (c *Client) post(ctx context.Context, path string, q url.Values, op Operation, payload []byte) ([]byte, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", boundedTransportError(err))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", boundedTransportError(err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	c.logger.DebugContext(ctx, "youtube api request", "method", "POST", "op", op)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, boundedRequestError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, youtubeAPIError(resp.StatusCode, body)
	}
	if c.recorder != nil {
		if err := c.recorder(ctx, op, 1); err != nil {
			return nil, fmt.Errorf("record quota: %w", err)
		}
	}

	return body, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, op Operation, calls int) ([]byte, error) {
	return c.getByID(ctx, path, q, op, calls, false)
}

// strict is used only by the by-ID completeness boundary; legacy reads keep
// their existing status, accounting and error behavior.
func (c *Client) getByID(ctx context.Context, path string, q url.Values, op Operation, calls int, strict bool) ([]byte, error) {
	observation := &rejectionObservation{operation: op}
	ctx = context.WithValue(ctx, rejectionObservationKey{}, observation)
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", boundedTransportError(err))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", boundedTransportError(err))
	}
	req.Header.Set("Accept", "application/json")

	c.logger.DebugContext(ctx, "youtube api request", "method", "GET", "op", op)
	if c.readPreflight != nil {
		if err := c.readPreflight(ctx, op); err != nil {
			return nil, err
		}
	}

	resp, err := c.httpClient.Do(req)
	if c.readRecorder != nil || c.quotaReads {
		return c.finishRead(ctx, resp, err, observation, op, strict)
	}
	if err != nil {
		return nil, boundedRequestError(err)
	}
	defer resp.Body.Close()
	// Count every Data API request that reached the provider, including 401.
	if c.recorder != nil {
		if err := c.recorder(ctx, op, calls); err != nil {
			return nil, fmt.Errorf("record quota: %w", err)
		}
	}

	body, err := io.ReadAll(resp.Body)
	var readErr error
	if resp.StatusCode >= 300 {
		if err != nil {
			readErr = youtubeReadAPIError(resp.StatusCode, nil)
		} else {
			readErr = youtubeReadAPIError(resp.StatusCode, body)
		}
	}
	observation.observeRejection(ctx, resp.StatusCode, readErr)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		if observation.err != nil {
			return nil, fmt.Errorf("%w (authorization renewal was unavailable)", readErr)
		}
		return nil, readErr
	}

	return body, nil
}

func (c *Client) finishRead(ctx context.Context, resp *http.Response, transportErr error, observation *rejectionObservation, op Operation, strict bool) ([]byte, error) {
	var body []byte
	var primary error
	var interruption error
	status := 0
	if transportErr != nil {
		if observation.admission.AdmittedAt.IsZero() {
			primary = boundedRequestError(transportErr)
		} else {
			primary = &ReadTransportError{Canceled: errors.Is(transportErr, context.Canceled) || errors.Is(transportErr, context.DeadlineExceeded)}
		}
	} else if resp != nil {
		defer resp.Body.Close()
		status = resp.StatusCode
		var err error
		body, err = io.ReadAll(resp.Body)
		switch {
		case errors.Is(err, context.Canceled), err != nil && errors.Is(ctx.Err(), context.Canceled):
			interruption = context.Canceled
		case errors.Is(err, context.DeadlineExceeded), err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
			interruption = context.DeadlineExceeded
		}
		if status >= 300 {
			if err != nil {
				body = nil // An incomplete rejection cannot establish a provider reason.
			}
			primary = youtubeReadAPIError(status, body)
		} else if err != nil {
			if strict {
				switch {
				case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
					primary = context.Canceled
				case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
					primary = context.DeadlineExceeded
				default:
					primary = invalidResponseError(nil, op)
				}
			} else {
				primary = errors.New("read YouTube response body failed")
			}
		} else if strict && status != http.StatusOK {
			primary = invalidResponseError(nil, op)
		}
	} else {
		primary = errors.New("youtube request returned no response")
	}
	accounting := observation.admission.Accounting
	if observation.admission.Finalize != nil {
		accounting = errors.Join(accounting, observation.admission.Finalize(ctx, readCategory(status, primary), status))
	} else if c.readRecorder != nil && !observation.admission.AdmittedAt.IsZero() {
		accounting = errors.Join(accounting, c.readRecorder(ctx, ReadAttempt{Operation: op, AdmittedAt: observation.admission.AdmittedAt, Status: status, Category: readCategory(status, primary)}))
	}
	if strict && ((resp != nil && observation.admission.AdmittedAt.IsZero()) ||
		(!observation.admission.AdmittedAt.IsZero() && observation.admission.Finalize == nil && c.readRecorder == nil)) {
		accounting = errors.Join(accounting, errors.New("youtube read admission or accounting unavailable"))
	}
	observation.observeRejection(ctx, status, primary)
	if accounting != nil || observation.err != nil || interruption != nil {
		return nil, &ReadError{Primary: primary, Accounting: accounting, Renewal: observation.err, Interruption: interruption, Status: status}
	}
	return body, primary
}

func (o *rejectionObservation) observeRejection(ctx context.Context, status int, readErr error) {
	if o.observe == nil || o.admission.AuthorizationID == "" {
		return
	}
	rejected := status == http.StatusUnauthorized
	if status == http.StatusForbidden {
		var api *APIError
		rejected = errors.As(readErr, &api) && api.Complete && len(api.Reasons) == 1 && api.Reasons[0] == "authError"
	}
	if rejected {
		o.err = o.observe(ctx, o.admission)
	}
}

func readCategory(status int, err error) string {
	if status >= 200 && status < 300 {
		return "success"
	}
	if status == 0 || status == 429 || status >= 500 {
		return "transient"
	}
	if status == 401 {
		return "auth"
	}
	var api *APIError
	if errors.As(err, &api) {
		switch api.Reason {
		case "authError":
			return "auth"
		case "quotaExceeded", "dailyLimitExceeded":
			return "quota"
		case "rateLimitExceeded", "userRateLimitExceeded":
			return "transient"
		}
	}
	return "permanent"
}

// boundedTransportError wraps a transport- or URL-construction failure
// without the request URL or query. net/url errors embed the full URL
// (including query parameters) in their message; that URL/query data must
// not cross diagnostic boundaries, so only the underlying error is kept
// (ARD-001).
func boundedTransportError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// boundedRequestError is the request-execution form of boundedTransportError.
func boundedRequestError(err error) error {
	return fmt.Errorf("execute request: %w", boundedTransportError(err))
}

// Read decisions share one interpretation of the provider error; mutation
// responses retain the legacy parser below.
func youtubeReadAPIError(status int, body []byte) error {
	api := &APIError{Status: status}
	if !utf8.Valid(body) {
		return api
	}
	root, ok := readJSONValue(body)
	if !ok {
		return api
	}
	errorObj, ok := readJSONField(root, "error")
	if !ok {
		return api
	}
	items, ok := readJSONField(errorObj, "errors")
	if !ok {
		return api
	}
	list, ok := items.([]any)
	if !ok || len(list) == 0 {
		return api
	}
	reasons := make([]string, 0, len(list))
	for _, item := range list {
		raw, ok := readJSONField(item, "reason")
		reason, valid := raw.(string)
		if !ok || !valid || !registeredProviderReason(reason) {
			return api
		}
		reasons = append(reasons, reason)
	}
	api.Complete = true
	api.Reasons = reasons
	if len(reasons) == 1 {
		api.Reason = reasons[0]
	}
	return api
}

// Field matching follows the case-insensitive provider schema, but rejects
// multiple spellings rather than choosing whichever appeared last.
func readJSONField(value any, field string) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	var result any
	found := false
	for key, candidate := range object {
		if strings.EqualFold(key, field) {
			if found {
				return nil, false
			}
			found, result = true, candidate
		}
	}
	return result, found
}

func readJSONValue(body []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value func() (any, error)
	value = func() (any, error) {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return token, nil
		}
		var result any
		switch delim {
		case '{':
			object := make(map[string]any)
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, errors.New("invalid JSON object key")
				}
				if _, exists := object[name]; exists {
					return nil, errors.New("duplicate JSON object key")
				}
				item, err := value()
				if err != nil {
					return nil, err
				}
				object[name] = item
			}
			result = object
		case '[':
			var items []any
			for dec.More() {
				item, err := value()
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			result = items
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
		_, err = dec.Token()
		return result, err
	}
	result, err := value()
	if err != nil {
		return nil, false
	}
	_, err = dec.Token()
	return result, errors.Is(err, io.EOF)
}

func youtubeAPIError(status int, body []byte) error {
	var payload struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	complete := json.Unmarshal(body, &payload) == nil && len(payload.Error.Errors) > 0
	reasons := make([]string, 0, len(payload.Error.Errors))
	if complete {
		for _, item := range payload.Error.Errors {
			if !registeredProviderReason(item.Reason) {
				complete = false
				break
			}
			reasons = append(reasons, item.Reason)
		}
	}
	if !complete {
		reasons = nil
	}
	reason := ""
	if len(reasons) == 1 {
		reason = reasons[0]
	}
	// The raw response body is deliberately discarded: only the status and a
	// vetted reason token are retained (ARD-001 failure diagnostic boundary).
	return &APIError{Status: status, Reason: reason, Reasons: reasons, Complete: complete}
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

var durationRe = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)

// parseDuration converts an ISO 8601 duration (PT1H2M3S) into seconds.
func parseDuration(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var total int
	if m[1] != "" {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m[2] != "" {
		min, _ := strconv.Atoi(m[2])
		total += min * 60
	}
	if m[3] != "" {
		sec, _ := strconv.Atoi(m[3])
		total += sec
	}
	return total, nil
}
