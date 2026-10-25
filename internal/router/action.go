package router

import "fmt"

// Public and internal representations of the playlist route action (ADR-007).
//
// The public API contract names the action PublicActionAddToPlaylist.
// Persisted route_action and executed_action rows, and configuration
// snapshots, keep the legacy internal spelling InternalActionAddToPlaylist.
// API-boundary adapters translate in both directions; stored records are
// never rewritten.
const (
	// PublicActionAddToPlaylist is the public API action_type value.
	PublicActionAddToPlaylist = "add_to_playlist"
	// InternalActionAddToPlaylist is the persisted and executed representation
	// of the playlist action.
	InternalActionAddToPlaylist = "playlist_insert"
)

// ToPublicActionType maps a stored action type to its public API
// representation. Values other than the internal playlist action are returned
// unchanged so no stored evidence is silently renamed.
func ToPublicActionType(stored string) string {
	if stored == InternalActionAddToPlaylist {
		return PublicActionAddToPlaylist
	}
	return stored
}

// FromPublicActionType maps a public action_type value to the internal
// representation. The public API accepts only the canonical public name; the
// internal spelling is not an input alias.
func FromPublicActionType(public string) (string, error) {
	if public != PublicActionAddToPlaylist {
		return "", fmt.Errorf("action_type %q is not supported; use %q", public, PublicActionAddToPlaylist)
	}
	return InternalActionAddToPlaylist, nil
}
