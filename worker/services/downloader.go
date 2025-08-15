package services

import (
	"encoding/json"
	"fmt"

	"github.com/meanii/downly/config"
)

type DownloaderService interface {
	// Download method to download content based on the provided URL
	Download() ([]string, error) // file paths or urls
}

type RabbitMQMessageType string

const (
	Cobalt RabbitMQMessageType = "cobalt"
	Ytdl   RabbitMQMessageType = "ytdl"
)

type RabbitMQMessage struct {
	ChatID           string `json:"chat_id"`
	ChatTitle        string `json:"chat_title"`
	MessageID        int    `json:"message_id"`
	FromUserID       int    `json:"from_user_id"`
	URL              string `json:"url"`
	Timestamp        string `json:"timestamp"`
	RepliedMessageID int    `json:"replied_message_id,omitempty"`
}

func Downloader(msg []byte, msgType RabbitMQMessageType) (DownloaderService, RabbitMQMessage, error) {
	// Parse the message to determine the service type
	var rabbitMQMessage RabbitMQMessage
	if err := json.Unmarshal(msg, &rabbitMQMessage); err != nil {
		return nil, RabbitMQMessage{}, fmt.Errorf("failed to unmarshal RabbitMQ message: %w", err)
	}
	switch msgType {
	case Cobalt:
		return &CobaltService{
			ApiUrl: config.ConfigS.Downly.Services.Cobalt.ApiUrl,
			Url:    rabbitMQMessage.URL,
		}, rabbitMQMessage, nil
	case Ytdl:
		return &YtdlpService{
			Bin: config.ConfigS.Downly.Services.Ytdl.Bin,
		}, rabbitMQMessage, nil
	default:
		return nil, RabbitMQMessage{}, fmt.Errorf("unsupported service type: %s", msgType)
	}
}
