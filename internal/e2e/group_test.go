package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/tgtest"
)

func supergroup(id int64) models.Chat { return models.Chat{ID: id, Type: models.ChatTypeSupergroup} }

func repliesTo(c tgtest.Call, messageID int) bool {
	return strings.Contains(c.Fields["reply_parameters"], fmt.Sprintf(`"message_id":%d`, messageID))
}

func (e *env) callsIn(method string, chatID int64) []tgtest.Call {
	var out []tgtest.Call
	for _, c := range e.api.CallsTo(method) {
		if c.Fields["chat_id"] == fmt.Sprint(chatID) {
			out = append(out, c)
		}
	}
	return out
}

// In a group, the bot answers the link message itself, stays quiet while
// downloading and removes its status message once the media is posted.
func TestE2EGroupAutoMode(t *testing.T) {
	e := newEnv(t)
	const g, member = -2001, 501
	linkMsg := e.send(supergroup(g), member, "look "+videoURL)
	job := e.waitJob(member)
	if job.Status != db.StatusDone || job.ReplyTo != int64(linkMsg) {
		t.Fatalf("job = %+v", job)
	}

	videos := e.callsIn("sendVideo", g)
	if len(videos) != 1 || !repliesTo(videos[0], linkMsg) {
		t.Fatalf("video should reply to the link message: %+v", videos)
	}
	acks := e.callsIn("sendMessage", g)
	if len(acks) != 1 || !repliesTo(acks[0], linkMsg) {
		t.Fatalf("status message should reply to the link: %+v", acks)
	}
	for _, text := range e.api.Texts(g) {
		if strings.Contains(text, "50%") {
			t.Fatalf("group got a percentage progress edit: %q", text)
		}
	}
	deleted := e.callsIn("deleteMessage", g)
	if len(deleted) != 1 || deleted[0].Fields["message_id"] != fmt.Sprint(job.TelegramMsgID) {
		t.Fatalf("status message not cleaned up: %+v", deleted)
	}
}

// Ordinary links in a busy group must not produce error messages.
func TestE2EGroupIgnoresNonMediaLinks(t *testing.T) {
	e := newEnv(t)
	const g, member = -2002, 502
	e.send(supergroup(g), member, "news: https://1.1.1.1/unsupported/article")
	job := e.waitJob(member)
	if job.Status != db.StatusFailed {
		t.Fatalf("job = %+v", job)
	}
	if len(e.callsIn("deleteMessage", g)) != 1 {
		t.Fatal("status message for a non-media link should be removed")
	}
	for _, text := range e.api.Texts(g) {
		if strings.Contains(text, "failed") || strings.Contains(text, "Error") {
			t.Fatalf("group got an error message: %q", text)
		}
	}
}

// In "command" mode plain links are ignored; /dl (or a /dl reply) downloads.
func TestE2EGroupCommandMode(t *testing.T) {
	e := newEnv(t)
	const g, admin, member = -2003, 503, 504
	e.api.SetChatAdmin(g, admin)

	e.press(supergroup(g), member, "set:gmode") // not an admin
	if mode, _ := db.GetGroupMode(context.Background(), e.pool, g); mode != db.GroupModeAuto {
		t.Fatal("a regular member changed the group mode")
	}
	e.press(supergroup(g), admin, "set:gmode")
	if mode, _ := db.GetGroupMode(context.Background(), e.pool, g); mode != db.GroupModeCommand {
		t.Fatalf("mode = %q", mode)
	}
	if !strings.Contains(e.api.LastText(g), "only with /dl") {
		t.Fatalf("settings not re-rendered: %q", e.api.LastText(g))
	}

	linkMsg := e.send(supergroup(g), member, videoURL)
	if jobs, _ := db.GetUserJobs(context.Background(), e.pool, member, 5); len(jobs) != 0 {
		t.Fatal("plain link downloaded in command mode")
	}

	orig := &models.Message{ID: linkMsg, Chat: supergroup(g), Text: videoURL}
	e.sendReply(supergroup(g), member, "/dl", orig)
	job := e.waitJob(member)
	if job.Status != db.StatusDone || job.ReplyTo != int64(linkMsg) {
		t.Fatalf("job = %+v", job)
	}
	if v := e.callsIn("sendVideo", g); len(v) != 1 || !repliesTo(v[0], linkMsg) {
		t.Fatalf("/dl reply should answer the original link: %+v", v)
	}

	e.send(supergroup(g), member, "/dl")
	if !strings.Contains(e.api.LastText(g), "Usage: /dl") {
		t.Fatalf("expected usage, got %q", e.api.LastText(g))
	}
}

// Private chats keep the old behaviour: no threaded replies, final status kept.
func TestE2EPrivateChatNotThreaded(t *testing.T) {
	e := newEnv(t)
	const user = 505
	e.send(private(user), user, videoURL)
	e.waitJob(user)
	if v := e.callsIn("sendVideo", user); len(v) != 1 || v[0].Fields["reply_parameters"] != "" {
		t.Fatalf("private video should not be a reply: %+v", v)
	}
	if len(e.callsIn("deleteMessage", user)) != 0 {
		t.Fatal("private status message should stay")
	}
}
