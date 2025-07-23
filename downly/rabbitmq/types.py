from pydantic import BaseModel, Field
from typing import Callable
from .callbacks.user_state_updater import user_state_updator
from .callbacks.chat_state_updater import chat_state_updator


class RabbitMQPublisherConfig(BaseModel):
    """
    Configuration for RabbitMQ publisher.
    """

    name: str = Field(..., description="Name of the publisher")
    exchange: str = Field(..., description="Name of the exchange")
    exchange_type: str = Field("direct", description="Type of the exchange", enumerate=["direct", "topic", "fanout", "headers"])
    durable: bool = Field(True, description="Durability of the exchange")
    routing_keys: dict[str, str] = Field(
        description="Routing keys for the publisher",
    )

class RabbitMQConsumerConfig(BaseModel):
    """
    Configuration for RabbitMQ consumer.
    """

    name: str = Field(..., description="Name of the consumer")
    queue: str = Field(..., description="Name of the queue")
    exchange: str = Field(..., description="Name of the exchange")
    durable: bool = Field(True, description="Durability of the queue")
    callback: Callable = Field(..., description="Callback function to process messages")
    routing_binding_key: str = Field(..., description="Routing binding key for the consumer")

    def get_queue_name(self) -> str:
        """
        Returns the name of the queue.
        """
        return self.queue

    def get_exchange_name(self) -> str:
        """
        Returns the name of the exchange.
        """
        return self.exchange

class AvailablePublishers:
    """
    Enum for available RabbitMQ publishers.
    This is used to register and retrieve publishers by name.
    """

    # chats and users stats updater
    DOWNLY_EVENT_STATS_UPDATER_PUBLISHER: RabbitMQPublisherConfig = RabbitMQPublisherConfig(
        name="DOWNLY_EVENT_STATS_UPDATER_PUBLISHER",
        exchange="downly.event.stats.updater.exchange",
        exchange_type="topic",
        durable=True,
        routing_keys = {
            "users": "downly.event.stats.updater.users.routing.key",
            "chats": "downly.event.stats.updater.chats.routing.key"
        }
    )
    
    # downly to worker adding queue
    DOWNLY_WORKER_QUEUE_PUBLISHER: RabbitMQPublisherConfig = RabbitMQPublisherConfig(
        name="DOWNLY_WORKER_QUEUE_PUBLISHER",
        exchange="downly.worker.queue.exchange",
        exchange_type="topic",
        durable=True,
        routing_keys = {
            "gallerydl": "downly.worker.queue.gallerydl.routing.key",
            "ytdl": "downly.worker.queue.ytdl.routing.key"
        }
    )


class AvailableConsumers:
    """
    Enum for available RabbitMQ consumers.
    This is used to register and retrieve consumers by name.
    """
    DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_EVENT_STATS_UPDATER_CONSUMER",
        queue="downly.event.stats.updater.users.queue",
        exchange="downly.event.stats.updater.exchange",
        durable=True,
        callback=user_state_updator,
        routing_binding_key="downly.event.stats.updater.users.routing.key"   # Use the first binding key for simplicity
    )
    
    DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER",
        queue="downly.event.stats.updater.chats.queue",
        exchange="downly.event.stats.updater.exchange",
        durable=True,
        callback=chat_state_updator,  # Replace with actual callback function
        routing_binding_key="downly.event.stats.updater.chats.routing.key"  # Use the first binding key for simplicity
    )
