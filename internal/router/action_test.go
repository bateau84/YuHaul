package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToPublicActionType(t *testing.T) {
	assert.Equal(t, PublicActionAddToPlaylist, ToPublicActionType(InternalActionAddToPlaylist))
	assert.Equal(t, "custom_action", ToPublicActionType("custom_action"),
		"unknown stored values pass through unchanged so no evidence is silently renamed")
}

func TestFromPublicActionType(t *testing.T) {
	internal, err := FromPublicActionType(PublicActionAddToPlaylist)
	require.NoError(t, err)
	assert.Equal(t, InternalActionAddToPlaylist, internal)

	for _, rejected := range []string{InternalActionAddToPlaylist, "", "Add_To_Playlist", "unknown"} {
		_, err := FromPublicActionType(rejected)
		assert.Error(t, err, "value %q must not be accepted as a public input alias", rejected)
	}
}
