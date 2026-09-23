package applemusic

import (
	"context"
	"errors"
)

// trackRef is how the API refers to a catalog song in a playlist body.
type trackRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type createPlaylistRequest struct {
	Attributes struct {
		Name string `json:"name"`
	} `json:"attributes"`
	Relationships struct {
		Tracks struct {
			Data []trackRef `json:"data"`
		} `json:"tracks"`
	} `json:"relationships"`
}

type createPlaylistResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// CreatePlaylist creates a library playlist called name containing songIDs,
// in order, in a single request. It returns the new playlist's ID.
func (c *Client) CreatePlaylist(ctx context.Context, name string, songIDs []string) (string, error) {
	var body createPlaylistRequest
	body.Attributes.Name = name
	body.Relationships.Tracks.Data = trackRefs(songIDs)

	var resp createPlaylistResponse
	if err := c.do(ctx, "POST", "/v1/me/library/playlists", nil, body, &resp); err != nil {
		return "", err
	}
	if len(resp.Data) == 0 || resp.Data[0].ID == "" {
		return "", errors.New("POST /v1/me/library/playlists: response has no playlist ID")
	}
	return resp.Data[0].ID, nil
}

// trackRefs turns song IDs into the {"id", "type": "songs"} objects the
// API expects. It never returns nil, so an empty list encodes as [] and
// not null.
func trackRefs(songIDs []string) []trackRef {
	refs := make([]trackRef, 0, len(songIDs))
	for _, id := range songIDs {
		refs = append(refs, trackRef{ID: id, Type: "songs"})
	}
	return refs
}
