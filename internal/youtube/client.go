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
)

// CostRecorder is invoked after each API call with the operation type and the
// number of calls represented by that request (one for most methods; one per
// batch for videos.list and channels.list).
type CostRecorder func(ctx context.Context, op Operation, calls int) error

// Client makes authenticated YouTube Data API v3 calls.
type Client struct {
	baseURL    string
	httpClient *http.Client
	recorder   CostRecorder
	logger     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the default production YouTube Data API base URL.
// It is used by tests.
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
		return nil, fmt.Errorf("decode subscriptions: %w", err)
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
		return nil, fmt.Errorf("decode channels: %w", err)
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
		return nil, "", fmt.Errorf("decode playlist items: %w", err)
	}

	out := make([]PlaylistItem, 0, len(raw.Items))
	for _, it := range raw.Items {
		if it.Snippet.ResourceID.VideoID == "" {
			continue
		}
		pub, err := parseTime(it.Snippet.PublishedAt)
		if err != nil {
			c.logger.DebugContext(ctx, "skipping playlist item with invalid publishedAt",
				"video_id", it.Snippet.ResourceID.VideoID,
				"published_at", it.Snippet.PublishedAt,
				"error", err)
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
		return nil, fmt.Errorf("decode videos: %w", err)
	}

	out := make([]VideoDetail, 0, len(raw.Items))
	for _, it := range raw.Items {
		pub, err := parseTime(it.Snippet.PublishedAt)
		if err != nil {
			c.logger.DebugContext(ctx, "skipping video with invalid publishedAt",
				"video_id", it.ID,
				"published_at", it.Snippet.PublishedAt,
				"error", err)
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
		return nil, fmt.Errorf("build url: %w", err)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	c.logger.DebugContext(ctx, "youtube api request", "method", "POST", "path", path, "op", op)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("youtube api %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	if c.recorder != nil {
		if err := c.recorder(ctx, op, 1); err != nil {
			return nil, fmt.Errorf("record quota: %w", err)
		}
	}

	return body, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, op Operation, calls int) ([]byte, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	c.logger.DebugContext(ctx, "youtube api request", "method", "GET", "path", path, "op", op)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("youtube api %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	if c.recorder != nil {
		if err := c.recorder(ctx, op, calls); err != nil {
			return nil, fmt.Errorf("record quota: %w", err)
		}
	}

	return body, nil
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
