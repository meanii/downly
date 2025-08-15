package rabbitmq

import (
	"fmt"
	"log"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

type RabbitMQClient struct {
	Username          string `yaml:"username"`
	Password          string `yaml:"password"`
	Host              string `yaml:"host"`
	Port              string `yaml:"Port"`
	Vhost             string `yaml:"Vhost"`
	HeartBeatInterval int    `yaml:"heartbeat_interval"`
	Connection        *amqp091.Connection
	Channel           *amqp091.Channel
}

func NewRabbitMQClient(config RabbitMQClient) (*RabbitMQClient, error) {
	var err error
	amqpURL := config.toAMPQURL()

	// creating amqp connection
	config.Connection, err = amqp091.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	// create channel connection
	config.Channel, err = config.Connection.Channel()
	if err != nil {
		return nil, err
	}

	return &config, err
}

func (ramq *RabbitMQClient) reconnect() error {
	var err error
	ramq.Connection, err = amqp091.Dial(ramq.toAMPQURL())
	if err != nil {
		return err
	}

	ramq.Channel, err = ramq.Connection.Channel()
	if err != nil {
		return err
	}
	return nil
}

func (ramq *RabbitMQClient) toAMPQURL() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%s/", ramq.Username, ramq.Password, ramq.Host, ramq.Port)
}

// Check connection and channel every heartbeat interval
func (ramq *RabbitMQClient) Check() {
	for {
		time.Sleep(time.Duration(ramq.HeartBeatInterval) * time.Second)
		if ramq.Connection != nil {
			log.Printf("rabbitmq connected: %v", !ramq.Connection.IsClosed())
		}
	}
}

// EnableDaemonReconnect running in background and checking for connection
// in every HeartBeatInterval
func (ramq *RabbitMQClient) EnableDaemonReconnect() {
	go func() {
		for {
			reason, ok := <-ramq.Connection.NotifyClose(
				make(chan *amqp091.Error),
			)
			if !ok {
				log.Print("rabbitmq connection closed")
				break
			}
			log.Printf("rabbitmq connection closed unexpectedly, reason: %v", reason)
			for {
				time.Sleep(time.Duration(ramq.HeartBeatInterval) * time.Second)
				err := ramq.reconnect()
				if err == nil {
					log.Println("rabbitmq reconnect success")
					break
				}
				log.Printf("rabbitmq reconnect failed, err: %v", err)
			}
		}
	}()
}
