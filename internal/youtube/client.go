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
	Primary    error
	Accounting error
	Renewal    error
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
		}
		if resp != nil && admission.AuthorizationID != "" && t.observe != nil {
			rejected := resp.StatusCode == http.StatusUnauthorized
			if resp.StatusCode == http.StatusForbidden {
				body, readErr := io.ReadAll(resp.Body)
				if readErr == nil {
					resp.Body.Close()
					resp.Body = io.NopCloser(bytes.NewReader(body))
					apiErr := youtubeAPIError(resp.StatusCode, body)
					var api *APIError
					rejected = errors.As(apiErr, &api) && api.Complete && len(api.Reasons) == 1 && api.Reasons[0] == "authError"
				}
			}
			if rejected {
				// Keep observer failure separate: net/http discards response bodies
				// returned alongside a RoundTripper error, losing read accounting.
				if err := t.observe(req.Context(), admission); err != nil {
					if state, ok := req.Context().Value(rejectionObservationKey{}).(*rejectionObservation); ok {
						state.err = err
					}
				}
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
		return c.finishRead(ctx, resp, err, observation, op)
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
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		readErr := youtubeAPIError(resp.StatusCode, body)
		if observation.err != nil {
			return nil, fmt.Errorf("%w (authorization renewal was unavailable)", readErr)
		}
		return nil, readErr
	}

	return body, nil
}

func (c *Client) finishRead(ctx context.Context, resp *http.Response, transportErr error, observation *rejectionObservation, op Operation) ([]byte, error) {
	var body []byte
	var primary error
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
		if status >= 300 {
			if err != nil {
				body = nil // An incomplete rejection cannot establish a provider reason.
			}
			primary = youtubeAPIError(status, body)
		} else if err != nil {
			primary = errors.New("read YouTube response body failed")
		}
	} else {
		primary = errors.New("youtube request returned no response")
	}
	var accounting error
	if observation.admission.Finalize != nil {
		accounting = observation.admission.Finalize(ctx, readCategory(status, primary), status)
	} else if c.readRecorder != nil && !observation.admission.AdmittedAt.IsZero() {
		accounting = c.readRecorder(ctx, ReadAttempt{Operation: op, AdmittedAt: observation.admission.AdmittedAt, Status: status, Category: readCategory(status, primary)})
	}
	if accounting != nil || observation.err != nil {
		return nil, &ReadError{Primary: primary, Accounting: accounting, Renewal: observation.err}
	}
	return body, primary
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
