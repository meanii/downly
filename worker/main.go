package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/meanii/downly/config"
	"github.com/meanii/downly/rabbitmq"
	"github.com/meanii/downly/services"
)

func main() {
	// parsing -config from args
	configPath := flag.String("config", "config.yaml", "config.yaml path")
	flag.Parse()

	// loading from passed config.yaml
	config, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalln(err)
	}

	rabbitmqClient, err := rabbitmq.NewRabbitMQClient(
		rabbitmq.RabbitMQClient{
			Username:          config.Downly.RabbitMQ.Username,
			Password:          config.Downly.RabbitMQ.Password,
			Host:              config.Downly.RabbitMQ.Host,
			Port:              config.Downly.RabbitMQ.Port,
			HeartBeatInterval: config.Downly.RabbitMQ.HeartBeatInterval,
		},
	)
	if err != nil {
		log.Fatalln(err)
	}

	go rabbitmqClient.Check()

	// declare publisher
	downlyPublisher, err := rabbitmq.NewRabbitMQAvailablePublisher(
		rabbitmqClient.Channel,
		rabbitmq.RabbitMQAvailablePublisher{
			Name:         "downly.worker.queue.downloader.publisher",
			Exchange:     "downly.worker.event.exchange",
			ExchangeType: "topic",
			Durable:      true,
		},
	)
	if err != nil {
		log.Fatalln(err)
	}

	// declaring cobalt consumer
	cobaltConsumer, err := rabbitmq.NewRabbitMQAvailableConsumer(
		rabbitmqClient.Channel,
		rabbitmq.RabbitMQAvailableConsumer{
			Name:              "downly.worker.queue.cobalt.consumer",
			Queue:             "downly.worker.queue.cobalt.queue",
			Exchange:          "downly.worker.queue.exchange",
			Durable:           true,
			RoutingBindingKey: "downly.worker.queue.cobalt.routing.key",
		},
	)
	if err != nil {
		log.Fatalln(err)
	}

	// declaring ytdl consumer
	ytdlConsumer, err := rabbitmq.NewRabbitMQAvailableConsumer(
		rabbitmqClient.Channel,
		rabbitmq.RabbitMQAvailableConsumer{
			Name:              "downly.worker.queue.ytdl.consumer",
			Queue:             "downly.worker.queue.ytdl.queue",
			Exchange:          "downly.worker.queue.exchange",
			Durable:           true,
			RoutingBindingKey: "downly.worker.queue.ytdl.routing.key",
		},
	)
	if err != nil {
		log.Fatalln(err)
	}

	// Build a welcome message.
	log.Println("Successfully connected to RabbitMQ")
	log.Println("Waiting for messages")

	// Make a channel to receive messages into infinite loop.
	forever := make(chan bool)

	go func() {
		for message := range cobaltConsumer.Message {
			// For example, show received message in a console.
			log.Printf(" > Received cobalt message: %s\n", message.Body)
			dlsvc, msg, err := services.Downloader(message.Body, services.Cobalt)

			if err != nil {
				message.Acknowledger.Nack(message.DeliveryTag, false, false)
				return
			}
			links, err := dlsvc.Download()
			if err != nil {
				fmt.Println("[Downloader] failed to download", err)
				return
			}
			fmt.Printf("[Downloader] downloaded links: %v\n", links)
			// create interface json message
			askMessage := map[string]any{
				"links":              links,
				"chat_id":            msg.ChatID,
				"chat_title":         msg.ChatTitle,
				"message_id":         msg.MessageID,
				"from_user_id":       msg.FromUserID,
				"replied_message_id": msg.RepliedMessageID,
			}
			err = downlyPublisher.Publish(askMessage, "downly.worker.event.success.routing.key")
			if err != nil {
				fmt.Println("[RabbitMQ.Publish] failed to publish", err)
			}

			err = message.Acknowledger.Ack(message.DeliveryTag, false)
			if err != nil {
				fmt.Println("[RabbitMQ.Ack] failed to ack", err)
			}

		}
	}()

	go func() {
		for message := range ytdlConsumer.Message {
			// For example, show received message in a console.
			log.Printf(" > Received ytdl message: %s\n", message.Body)
			err := message.Acknowledger.Ack(message.DeliveryTag, false)
			if err != nil {
				fmt.Println("[RabbitMQ.Ack] failed to ack")
			}

		}
	}()

	defer func() {
		if err := rabbitmqClient.Connection.Close(); err != nil {
			log.Print(err)
		}
		log.Print("rabbitmq connection closed")
	}()

	rabbitmqClient.EnableDaemonReconnect()

	<-forever
}
