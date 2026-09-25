package client

import "context"

// Profile photos and sending a photo (docs/adr/0025-life-and-legacy.md: a
// player card may show the player's Telegram profile photo).
//
// Bot API, getUserProfilePhotos: "Use this method to get a list of profile
// pictures for a user. Returns a UserProfilePhotos object." — user_id, and
// limit 1..100. UserProfilePhotos.photos is "Array of Array of PhotoSize",
// each photo "in up to 4 sizes". A file_id "is unique for each individual bot
// and can't be transferred from one bot to another" (Sending files), which is
// why what one bot fetched is kept for that bot alone. sendPhoto takes a
// file_id as photo and a caption of 0-1024 characters after entities
// parsing.

// PhotoSize is one size of a photo.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// UserProfilePhotos is a user's profile pictures.
type UserProfilePhotos struct {
	TotalCount int           `json:"total_count"`
	Photos     [][]PhotoSize `json:"photos"`
}

type getUserProfilePhotosRequest struct {
	UserID int64 `json:"user_id"`
	Limit  int   `json:"limit,omitempty"`
}

// GetUserProfilePhotos returns up to limit of a user's profile pictures,
// newest first.
func (c *Client) GetUserProfilePhotos(ctx context.Context, userID int64, limit int) (*UserProfilePhotos, error) {
	var out UserProfilePhotos
	if err := c.do(ctx, c.httpClient, "getUserProfilePhotos", nil,
		getUserProfilePhotosRequest{UserID: userID, Limit: limit}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LatestProfilePhoto is the file id of the largest size of a user's newest
// profile picture, empty when they have none this bot may see.
func (c *Client) LatestProfilePhoto(ctx context.Context, userID int64) (string, error) {
	photos, err := c.GetUserProfilePhotos(ctx, userID, 1)
	if err != nil || photos == nil || len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		return "", err
	}
	best := photos.Photos[0][0]
	for _, p := range photos.Photos[0] {
		if p.Width*p.Height > best.Width*best.Height {
			best = p
		}
	}
	return best.FileID, nil
}

// MaxCaptionRunes is the most a photo's caption may hold.
const MaxCaptionRunes = 1024

type sendPhotoRequest struct {
	ChatID      int64  `json:"chat_id"`
	Photo       string `json:"photo"`
	Caption     string `json:"caption,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

// SendPhoto sends a photo the bot already knows by file id, with a caption
// and a keyboard.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, fileID, caption string, replyMarkup any) (*Message, error) {
	var sent Message
	if err := c.do(ctx, c.httpClient, "sendPhoto", nil,
		sendPhotoRequest{ChatID: chatID, Photo: fileID, Caption: caption, ReplyMarkup: replyMarkup}, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}
