package rabbitmq

import (
	"encoding/json"

	"github.com/rabbitmq/amqp091-go"
)

type RabbitMQAvailablePublisher struct {
	Name         string
	Exchange     string
	ExchangeType string
	Durable      bool

	// channel is the AMQP channel used to publish messages
	channel *amqp091.Channel
}

// NewRabbitMQAvailablePublisher creates a new RabbitMQAvailablePublisher instance
func NewRabbitMQAvailablePublisher(
	channel *amqp091.Channel,
	publishConfig RabbitMQAvailablePublisher,
) (*RabbitMQAvailablePublisher, error) {

	// exchange declare if not exists
	channel.ExchangeDeclare(
		publishConfig.Exchange,
		publishConfig.ExchangeType,
		publishConfig.Durable,
		false,
		false,
		false,
		nil,
	)
	publishConfig.channel = channel
	return &publishConfig, nil
}

// Publish a message to the RabbitMQ exchange
func (r *RabbitMQAvailablePublisher) Publish(
	message map[string]any,
	routingKey string,
) error {
	// convert message to JSON
	messageJSON, err := json.Marshal(message)
	if err != nil {
		return err
	}

	err = r.channel.Publish(
		r.Exchange,
		routingKey,
		false,
		false,
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        messageJSON,
		},
	)
	return err
}
