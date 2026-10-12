package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/util/random"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"
)

// phoneAnswer encrypts a media retry notification the way the phone does.
func phoneAnswer(t *testing.T, messageID string, mediaKey []byte, notification *waMmsRetry.MediaRetryNotification) *events.MediaRetry {
	t.Helper()
	plaintext, err := proto.Marshal(notification)
	if err != nil {
		t.Fatal(err)
	}
	iv := random.Bytes(12)
	key := hkdfutil.SHA256(mediaKey, nil, []byte("WhatsApp Media Retry Notification"), 32)
	ciphertext, err := gcmutil.Encrypt(key, iv, plaintext, []byte(messageID))
	if err != nil {
		t.Fatal(err)
	}
	return &events.MediaRetry{MessageID: types.MessageID(messageID), Ciphertext: ciphertext, IV: iv}
}

func TestReuploadedDirectPath(t *testing.T) {
	mediaKey := random.Bytes(32)
	info := &types.MessageInfo{ID: "AUDIO1"}
	answers := func(evt *events.MediaRetry) func(*types.MessageInfo, []byte) error {
		return func(*types.MessageInfo, []byte) error {
			go handleMediaRetry(evt)
			return nil
		}
	}

	path, err := reuploadedDirectPath(answers(phoneAnswer(t, "AUDIO1", mediaKey, &waMmsRetry.MediaRetryNotification{
		StanzaID:   proto.String("AUDIO1"),
		DirectPath: proto.String("/v/t62.7117-24/new.enc?ccb=11-4&oh=x&oe=y"),
		Result:     waMmsRetry.MediaRetryNotification_SUCCESS.Enum(),
	})), info, mediaKey, time.Second)
	if err != nil || path != "/v/t62.7117-24/new.enc?ccb=11-4&oh=x&oe=y" {
		t.Errorf("success: got %q, %v", path, err)
	}

	_, err = reuploadedDirectPath(answers(phoneAnswer(t, "AUDIO1", mediaKey, &waMmsRetry.MediaRetryNotification{
		Result: waMmsRetry.MediaRetryNotification_NOT_FOUND.Enum(),
	})), info, mediaKey, time.Second)
	if err == nil || !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("not found: got %v", err)
	}

	_, err = reuploadedDirectPath(answers(&events.MediaRetry{
		MessageID: "AUDIO1", Error: &events.MediaRetryError{Code: 2},
	}), info, mediaKey, time.Second)
	if !errors.Is(err, whatsmeow.ErrMediaNotAvailableOnPhone) {
		t.Errorf("deleted from phone: got %v", err)
	}

	silent := func(*types.MessageInfo, []byte) error { return nil }
	_, err = reuploadedDirectPath(silent, info, mediaKey, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("phone offline: got %v", err)
	}

	// An answer for another message is not mistaken for this one
	other := answers(&events.MediaRetry{MessageID: "OTHER", Error: &events.MediaRetryError{Code: 2}})
	_, err = reuploadedDirectPath(other, info, mediaKey, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("other message: got %v", err)
	}
}

func TestRetryMessageInfo(t *testing.T) {
	info, err := retryMessageInfo("A", "555199330185@s.whatsapp.net", "555199330185", false)
	if err != nil || info.IsGroup || info.Chat.String() != "555199330185@s.whatsapp.net" || !info.Sender.IsEmpty() {
		t.Errorf("direct chat: %+v, %v", info, err)
	}
	for _, sender := range []string{"5511999", "5511999@s.whatsapp.net"} {
		info, err = retryMessageInfo("B", "1203@g.us", sender, false)
		if err != nil || !info.IsGroup || info.Sender.String() != "5511999@s.whatsapp.net" {
			t.Errorf("group sender %q: %+v, %v", sender, info, err)
		}
	}
	info, err = retryMessageInfo("C", "555199330185@s.whatsapp.net", "", true)
	if err != nil || !info.IsFromMe {
		t.Errorf("from me: %+v, %v", info, err)
	}
}
