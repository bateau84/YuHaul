package youtube

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetVideosWithReturnedIDsLiveStatus covers the classification rules of
// live-status ARS-001 §2, in rule order.
func TestGetVideosWithReturnedIDsLiveStatus(t *testing.T) {
	const pub = `"publishedAt":"2024-01-01T00:00:00Z"`
	end := `"actualEndTime":"2024-01-01T02:00:00Z"`
	for _, tc := range []struct {
		name, item string
		want       LiveStatus
	}{
		{"live", `{"id":"v1","snippet":{` + pub + `,"liveBroadcastContent":"live"}}`, LiveNow},
		{"upcoming", `{"id":"v1","snippet":{` + pub + `,"liveBroadcastContent":"upcoming"}}`, LiveUpcoming},
		{"none without details", `{"id":"v1","snippet":{` + pub + `,"liveBroadcastContent":"none"},"contentDetails":{"duration":"PT5M"}}`, LiveNone},
		{"none with ended stream", `{"id":"v1","snippet":{` + pub + `,"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualStartTime":"2024-01-01T01:00:00Z",` + end + `}}`, LiveNone},
		{"none with ended stream and no start", `{"id":"v1","snippet":{` + pub + `,"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `}}`, LiveNone},
		{"missing lbc", `{"id":"v1","snippet":{` + pub + `}}`, LiveUnconfirmed},
		{"missing duration and lbc", `{"id":"v1","snippet":{` + pub + `},"contentDetails":{"duration":"P0D"}}`, LiveUnconfirmed},
		{"missing snippet", `{"id":"v1"}`, LiveUnconfirmed},
		{"snippet not object", `{"id":"v1","snippet":[]}`, LiveUnconfirmed},
		{"duplicate snippet", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"snippet":{"liveBroadcastContent":"none"}}`, LiveUnconfirmed},
		{"duplicate lbc", `{"id":"v1","snippet":{"liveBroadcastContent":"none","liveBroadcastContent":"none"}}`, LiveUnconfirmed},
		{"lbc not string", `{"id":"v1","snippet":{"liveBroadcastContent":1}}`, LiveUnconfirmed},
		{"lbc null", `{"id":"v1","snippet":{"liveBroadcastContent":null}}`, LiveUnconfirmed},
		{"lbc unknown", `{"id":"v1","snippet":{"liveBroadcastContent":"completed"}}`, LiveUnconfirmed},
		{"lbc case", `{"id":"v1","snippet":{"liveBroadcastContent":"None"}}`, LiveUnconfirmed},
		{"details duplicate member", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `},"liveStreamingDetails":{` + end + `}}`, LiveUnconfirmed},
		{"details not object", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":null}`, LiveUnconfirmed},
		{"details with duplicate inner member", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `,` + end + `}}`, LiveUnconfirmed},
		{"details without end", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualStartTime":"2024-01-01T01:00:00Z"}}`, LiveUnconfirmed},
		{"details with scheduled start only", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"scheduledStartTime":"2024-01-01T01:00:00Z"}}`, LiveUnconfirmed},
		{"end unparsable", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualEndTime":"soon"}}`, LiveUnconfirmed},
		{"end not string", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualEndTime":5}}`, LiveUnconfirmed},
		{"active chat", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `,"activeLiveChatId":"c"}}`, LiveUnconfirmed},
		{"concurrent viewers", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `,"concurrentViewers":"3"}}`, LiveUnconfirmed},
		{"active chat null", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `,"activeLiveChatId":null}}`, LiveUnconfirmed},
		{"concurrent viewers null", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{` + end + `,"concurrentViewers":null}}`, LiveUnconfirmed},
		{"start equals end", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualStartTime":"2024-01-01T02:00:00Z",` + end + `}}`, LiveNone},
		{"start after end", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualStartTime":"2024-01-01T03:00:00Z",` + end + `}}`, LiveUnconfirmed},
		{"start unparsable", `{"id":"v1","snippet":{"liveBroadcastContent":"none"},"liveStreamingDetails":{"actualStartTime":"x",` + end + `}}`, LiveUnconfirmed},
		// Rules 3 wins over later rules: live is live even with odd details.
		{"live ignores details", `{"id":"v1","snippet":{"liveBroadcastContent":"live"},"liveStreamingDetails":null}`, LiveNow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newAccountedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "snippet,contentDetails,liveStreamingDetails", r.URL.Query().Get("part"))
				_, _ = io.WriteString(w, `{"items":[`+tc.item+`]}`)
			})
			_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			require.NoError(t, err)
			require.True(t, ids.Complete)
			assert.Equal(t, map[string]LiveStatus{"v1": tc.want}, ids.Live)
		})
	}
}

// A returned item whose publishedAt is unusable still gets a Live entry.
func TestGetVideosWithReturnedIDsLiveEntryWithoutUsableDetail(t *testing.T) {
	c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":"v1","snippet":{"publishedAt":"bad","liveBroadcastContent":"upcoming"}}]}`)
	})
	details, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	require.NoError(t, err)
	assert.Empty(t, details)
	assert.Equal(t, map[string]LiveStatus{"v1": LiveUpcoming}, ids.Live)
}

// The legacy GetVideos keeps its request shape.
func TestGetVideosLegacyPartUnchanged(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "snippet,contentDetails", r.URL.Query().Get("part"))
		_, _ = io.WriteString(w, `{"items":[]}`)
	})
	_, err := c.GetVideos(context.Background(), []string{"v1"})
	require.NoError(t, err)
}
