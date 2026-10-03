// Package tgtest provides a fake Telegram Bot API server for tests. It
// records every call (including uploaded files) and answers with minimal but
// well-formed responses.
package tgtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Call is one recorded API request.
type Call struct {
	Method string
	Fields map[string]string
	// Files maps form field (e.g. "video") to the uploaded file.
	Files map[string]File
}

// File is an uploaded file.
type File struct {
	Name string
	Data []byte
}

// Server is a fake Bot API.
type Server struct {
	srv *httptest.Server

	mu         sync.Mutex
	calls      []Call
	nextMsgID  int
	forbidden  map[int64]bool
	chatAdmins map[string]bool
	notify     chan struct{}
}

// New starts a fake API server, closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{forbidden: map[int64]bool{}, chatAdmins: map[string]bool{}, notify: make(chan struct{}, 1)}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server URL to pass to bot.WithServerURL.
func (s *Server) URL() string { return s.srv.URL }

// SetForbidden makes every call targeting chatID fail with 403, as when a
// user blocked the bot.
func (s *Server) SetForbidden(chatID int64, v bool) {
	s.mu.Lock()
	s.forbidden[chatID] = v
	s.mu.Unlock()
}

// SetChatAdmin makes getChatMember report userID as an administrator of chatID.
func (s *Server) SetChatAdmin(chatID, userID int64) {
	s.mu.Lock()
	s.chatAdmins[fmt.Sprintf("%d:%d", chatID, userID)] = true
	s.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	_ = r.ParseMultipartForm(64 << 20)
	call := Call{Method: method, Fields: map[string]string{}, Files: map[string]File{}}
	if r.MultipartForm != nil {
		for k, v := range r.MultipartForm.Value {
			call.Fields[k] = v[0]
		}
		for k, fhs := range r.MultipartForm.File {
			if f, err := fhs[0].Open(); err == nil {
				data, _ := io.ReadAll(f)
				_ = f.Close()
				call.Files[k] = File{Name: fhs[0].Filename, Data: data}
			}
		}
	}
	chatID, _ := strconv.ParseInt(call.Fields["chat_id"], 10, 64)

	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.nextMsgID++
	id := s.nextMsgID
	blocked := s.forbidden[chatID]
	isAdmin := s.chatAdmins[call.Fields["chat_id"]+":"+call.Fields["user_id"]]
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}

	w.Header().Set("Content-Type", "application/json")
	if blocked {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
		return
	}
	message := map[string]any{"message_id": id, "date": 0, "chat": map[string]any{"id": chatID, "type": "private"}, "text": call.Fields["text"]}
	var result any
	switch method {
	case "getMe":
		result = map[string]any{"id": 1, "is_bot": true, "first_name": "Downly", "username": "downly_test_bot"}
	case "sendMessage", "editMessageText", "sendVideo", "sendAudio", "sendPhoto", "sendDocument":
		result = message
	case "getChatMember":
		status := "member"
		if isAdmin {
			status = "administrator"
		}
		uid, _ := strconv.ParseInt(call.Fields["user_id"], 10, 64)
		result = map[string]any{"status": status, "user": map[string]any{"id": uid, "is_bot": false, "first_name": "U"}}
	default:
		result = true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// Calls returns a copy of every recorded call.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// CallsTo returns the recorded calls of one API method.
func (s *Server) CallsTo(method string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Texts returns the text of every sendMessage/editMessageText to chatID.
func (s *Server) Texts(chatID int64) []string {
	var out []string
	id := strconv.FormatInt(chatID, 10)
	for _, c := range s.Calls() {
		if (c.Method == "sendMessage" || c.Method == "editMessageText") && c.Fields["chat_id"] == id {
			out = append(out, c.Fields["text"])
		}
	}
	return out
}

// AnyText reports whether any text sent to chatID contains substr.
func (s *Server) AnyText(chatID int64, substr string) bool {
	return s.Count(chatID, substr) > 0
}

// Count counts texts sent to chatID that contain substr.
func (s *Server) Count(chatID int64, substr string) int {
	n := 0
	for _, t := range s.Texts(chatID) {
		if strings.Contains(t, substr) {
			n++
		}
	}
	return n
}

// LastText returns the last text sent or edited in chatID.
func (s *Server) LastText(chatID int64) string {
	t := s.Texts(chatID)
	if len(t) == 0 {
		return ""
	}
	return t[len(t)-1]
}

// LastMarkup returns the reply_markup JSON of the last message with buttons
// sent or edited in chatID.
func (s *Server) LastMarkup(chatID int64) string {
	calls := s.Calls()
	id := strconv.FormatInt(chatID, 10)
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		if (c.Method == "sendMessage" || c.Method == "editMessageText") && c.Fields["chat_id"] == id && c.Fields["reply_markup"] != "" {
			return c.Fields["reply_markup"]
		}
	}
	return ""
}

// Changed is signalled (best effort) after each call; tests can select on
// it while polling for a condition.
func (s *Server) Changed() <-chan struct{} { return s.notify }
