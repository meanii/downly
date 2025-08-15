from pydantic import BaseModel, Field
from typing import Callable
from .callbacks.user_state_updater import user_state_updator
from .callbacks.chat_state_updater import chat_state_updator
from .callbacks.worker_success_event import downly_success_event


class RabbitMQPublisherConfig(BaseModel):
    """
    Configuration for RabbitMQ publisher.
    """

    name: str = Field(..., description="Name of the publisher")
    exchange: str = Field(..., description="Name of the exchange")
    exchange_type: str = Field(
        "direct",
        description="Type of the exchange",
        enumerate=["direct", "topic", "fanout", "headers"],
    )
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
    routing_binding_key: str = Field(
        ..., description="Routing binding key for the consumer"
    )

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
    DOWNLY_EVENT_STATS_UPDATER_PUBLISHER: RabbitMQPublisherConfig = (
        RabbitMQPublisherConfig(
            name="DOWNLY_EVENT_STATS_UPDATER_PUBLISHER",
            exchange="downly.event.stats.updater.exchange",
            exchange_type="topic",
            durable=True,
            routing_keys={
                "users": "downly.event.stats.updater.users.routing.key",
                "chats": "downly.event.stats.updater.chats.routing.key",
            },
        )
    )

    # downly to worker adding queue
    DOWNLY_WORKER_QUEUE_PUBLISHER: RabbitMQPublisherConfig = RabbitMQPublisherConfig(
        name="DOWNLY_WORKER_QUEUE_PUBLISHER",
        exchange="downly.worker.queue.exchange",
        exchange_type="topic",
        durable=True,
        routing_keys={
            "cobalt": "downly.worker.queue.cobalt.routing.key",
            "ytdl": "downly.worker.queue.ytdl.routing.key",
        },
    )

def dummy_callback(channel, method, properties, body):
    """
    A dummy callback function for RabbitMQ consumers.
    """
    
    channel.basic_ack(delivery_tag=method.delivery_tag)

class AvailableConsumers:
    """
    Enum for available RabbitMQ consumers.
    This is used to register and retrieve consumers by name.
    """

    DOWNLY_WORKER_EVENT_SUCCESS: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_WORKER_EVENT_SUCCESS",
        queue="downly.worker.event.success.queue",
        exchange="downly.worker.event.exchange",
        durable=True,
        callback=downly_success_event,
        routing_binding_key="downly.worker.event.success.routing.key",
    )

    DOWNLY_WORKER_EVENT_FAILED: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_WORKER_EVENT_FAILED",
        queue="downly.worker.event.failed.queue",
        exchange="downly.worker.event.exchange",
        durable=True,
        callback=dummy_callback,  # Replace with actual callback function
        routing_binding_key="downly.worker.event.failed.routing.key",
    )

    DOWNLY_WORKER_EVENT_PROGRESS: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_WORKER_EVENT_PROGRESS",
        queue="downly.worker.event.progress.queue",
        exchange="downly.worker.event.exchange",
        durable=True,
        callback=dummy_callback,  # Replace with actual callback function
        routing_binding_key="downly.worker.event.progress.routing.key",
    )

    DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_EVENT_STATS_UPDATER_CONSUMER",
        queue="downly.event.stats.updater.users.queue",
        exchange="downly.event.stats.updater.exchange",
        durable=True,
        callback=user_state_updator,
        routing_binding_key="downly.event.stats.updater.users.routing.key",  # Use the first binding key for simplicity
    )

    DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER: RabbitMQConsumerConfig = RabbitMQConsumerConfig(
        name="DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER",
        queue="downly.event.stats.updater.chats.queue",
        exchange="downly.event.stats.updater.exchange",
        durable=True,
        callback=chat_state_updator,  # Replace with actual callback function
        routing_binding_key="downly.event.stats.updater.chats.routing.key",  # Use the first binding key for simplicity
    )