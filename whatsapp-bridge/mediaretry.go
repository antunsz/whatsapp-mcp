package main

// WhatsApp drops media from its servers after a while, and downloads then fail
// with 403, 404 or 410. The phone usually still has the file: a media retry
// receipt asks it to upload the file again, and it answers with a new direct
// path, encrypted with the media key.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const mediaRetryTimeout = 30 * time.Second

// pendingRetries maps a message ID to the channel of the download waiting for
// the phone's answer.
var pendingRetries sync.Map

// handleMediaRetry hands the phone's answer to the download waiting for it.
func handleMediaRetry(evt *events.MediaRetry) {
	if answer, ok := pendingRetries.Load(evt.MessageID); ok {
		select {
		case answer.(chan *events.MediaRetry) <- evt:
		default:
		}
	}
}

// isExpiredMedia reports whether a download failed because WhatsApp no longer
// has the file.
func isExpiredMedia(err error) bool {
	return errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410)
}

// downloadFromPhone asks the phone to upload a message's media again and
// downloads it from the new location.
func downloadFromPhone(client *whatsmeow.Client, messageStore *MessageStore, downloader *MediaDownloader, messageID, chatJID string) ([]byte, error) {
	var sender string
	var isFromMe bool
	err := messageStore.db.QueryRow(
		"SELECT sender, is_from_me FROM messages WHERE id = ? AND chat_jid = ?", messageID, chatJID,
	).Scan(&sender, &isFromMe)
	if err != nil {
		return nil, err
	}
	info, err := retryMessageInfo(messageID, chatJID, sender, isFromMe)
	if err != nil {
		return nil, err
	}
	send := func(info *types.MessageInfo, mediaKey []byte) error {
		return client.SendMediaRetryReceipt(context.Background(), info, mediaKey)
	}
	directPath, err := reuploadedDirectPath(send, info, downloader.MediaKey, mediaRetryTimeout)
	if err != nil {
		return nil, err
	}
	downloader.DirectPath = directPath
	return client.Download(context.Background(), downloader)
}

// reuploadedDirectPath sends a media retry receipt and waits for the phone to
// answer with the media's new direct path.
func reuploadedDirectPath(send func(*types.MessageInfo, []byte) error, info *types.MessageInfo, mediaKey []byte, timeout time.Duration) (string, error) {
	answer := make(chan *events.MediaRetry, 1)
	pendingRetries.Store(info.ID, answer)
	defer pendingRetries.Delete(info.ID)

	if err := send(info, mediaKey); err != nil {
		return "", fmt.Errorf("could not ask the phone for the media: %w", err)
	}
	select {
	case evt := <-answer:
		notification, err := whatsmeow.DecryptMediaRetryNotification(evt, mediaKey)
		if err != nil {
			return "", err
		}
		if notification.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS {
			return "", fmt.Errorf("the phone could not upload the media again: %s", notification.GetResult())
		}
		return notification.GetDirectPath(), nil
	case <-time.After(timeout):
		return "", fmt.Errorf("the phone did not answer within %s (it must be online)", timeout)
	}
}

// retryMessageInfo describes a stored message the way a media retry receipt
// needs it. Group senders are stored either bare ("5511...") or as a JID.
func retryMessageInfo(messageID, chatJID, sender string, isFromMe bool) (*types.MessageInfo, error) {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return nil, err
	}
	info := &types.MessageInfo{
		ID: messageID,
		MessageSource: types.MessageSource{
			Chat:     chat,
			IsFromMe: isFromMe,
			IsGroup:  chat.Server == types.GroupServer,
		},
	}
	if info.IsGroup {
		if strings.Contains(sender, "@") {
			if info.Sender, err = types.ParseJID(sender); err != nil {
				return nil, err
			}
		} else {
			info.Sender = types.NewJID(sender, types.DefaultUserServer)
		}
	}
	return info, nil
}
