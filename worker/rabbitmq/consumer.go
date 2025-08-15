package rabbitmq

import "github.com/rabbitmq/amqp091-go"

// RabbitMQAvailableConsumer this contain consumer config
// and later it will use to auto configure consumer for worker
type RabbitMQAvailableConsumer struct {
	Name              string
	Queue             string
	Exchange          string
	Durable           bool
	Message           <-chan amqp091.Delivery
	RoutingBindingKey string
}

func NewRabbitMQAvailableConsumer(
	channel *amqp091.Channel,
	consumerConfig RabbitMQAvailableConsumer,
) (*RabbitMQAvailableConsumer, error) {
	// consumer config struck
	consumer := &RabbitMQAvailableConsumer{
		Name:              consumerConfig.Name,
		Queue:             consumerConfig.Queue,
		Exchange:          consumerConfig.Exchange,
		Durable:           consumerConfig.Durable,
		RoutingBindingKey: consumerConfig.RoutingBindingKey,
	}

	// create queue if not exists
	_, err := channel.QueueDeclare(
		consumer.Queue,
		consumer.Durable,
		false, // auto delete
		false, // exclusive
		false, // no wait
		nil,   // auguments
	)
	if err != nil {
		return nil, err
	}

	// queue binding with exchange
	err = channel.QueueBind(
		consumer.Queue,             // queue
		consumer.RoutingBindingKey, // routing key binding
		consumer.Exchange,          // exchange
		false,                      // no wait
		nil,                        // arguments
	)
	if err != nil {
		return nil, err
	}

	// declaring queue, and adding to message chan
	consumer.Message, err = channel.Consume(
		consumer.Queue,
		consumer.Queue, // consumer name
		false,          // auto ack
		false,          // exclusive
		false,          // no-local
		false,          // no wait
		nil,            // arguments
	)

	return consumer, err
}
