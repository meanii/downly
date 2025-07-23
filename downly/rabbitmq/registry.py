import threading
from time import sleep
from downly.rabbitmq.publisher import RabbitMQPublisher
from downly.rabbitmq.consumer import RabbitMQConsumer
from downly.rabbitmq.types import AvailablePublishers, AvailableConsumers
from downly.rabbitmq.client import RabbitMQConnectionManagerSingleton
from loguru import logger
from typing import Union, Callable, Dict


class RabbitMQPublisherRegistry:
    """
    Registry for RabbitMQ publishers.
    """
    _publishers: dict[str, RabbitMQPublisher] = {}

    @classmethod
    def register_publisher(cls, name: str, publisher: RabbitMQPublisher):
        """
        Register a RabbitMQ publisher.
        """
        logger.info(f"Registering publisher: {name}")
        if not isinstance(publisher, RabbitMQPublisher):
            logger.error(f"Publisher must be an instance of RabbitMQPublisher, got {type(publisher)}")
            raise TypeError("Publisher must be an instance of RabbitMQPublisher")
        if name in cls._publishers:
            logger.warning(f"Publisher with name {name} already exists, replacing it")
        else:
            cls._publishers[name] = publisher
            logger.info(f"Publisher {name} registered successfully")

    @classmethod
    def get_publisher(cls, name: str) -> Union[RabbitMQPublisher, None]:
        """
        Get a RabbitMQ publisher by name.
        """
        logger.info(f"Retrieving publisher: {name}")
        publisher = cls._publishers.get(name)
        if not publisher:
            logger.warning(f"Publisher with name {name} not found")
        return publisher


class RabbitMQConsumerRegistry:
    """
    Registry for managing RabbitMQ consumers with lifecycle control.
    """
    _consumers: Dict[str, RabbitMQConsumer] = {}
    _runner_thread = None
    _stop_runner = threading.Event()

    @classmethod
    def register_consumer(
        cls,
        name: str,
        connection_manager: RabbitMQConnectionManagerSingleton,
        queue: str,
        exchange: str,
        routing_key: str,
        callback: Callable,
        durable: bool = True
    ):
        """Register and start a new consumer"""
        if name in cls._consumers:
            logger.warning(f"Consumer '{name}' already exists. Replacing...")
            cls._consumers[name].stop()
        
        consumer = RabbitMQConsumer(
            connection_manager=connection_manager,
            queue=queue,
            exchange=exchange,
            routing_key=routing_key,
            callback=callback,
            durable=durable
        )
        
        cls._consumers[name] = consumer
        consumer.start()
        logger.info(f"Registered and started consumer: {name}")

    @classmethod
    def get_consumer(cls, name: str) -> RabbitMQConsumer:
        """Get a registered consumer"""
        return cls._consumers.get(name)

    @classmethod
    def start_all(cls):
        """Start all registered consumers"""
        for name, consumer in cls._consumers.items():
            consumer.start()

    @classmethod
    def stop_all(cls):
        """Stop all registered consumers gracefully"""
        for name, consumer in cls._consumers.items():
            consumer.stop()

    @classmethod
    def start_runner(cls):
        """Start a monitoring thread for all consumers"""
        if cls._runner_thread and cls._runner_thread.is_alive():
            logger.warning("Consumer runner is already running")
            return
        
        cls._stop_runner.clear()
        cls._runner_thread = threading.Thread(
            target=cls._monitor_consumers,
            daemon=True
        )
        cls._runner_thread.start()
        logger.info("Consumer runner started")

    @classmethod
    def stop_runner(cls):
        """Stop the monitoring thread"""
        cls._stop_runner.set()
        if cls._runner_thread and cls._runner_thread.is_alive():
            cls._runner_thread.join(timeout=5)
            logger.info("Consumer runner stopped")

    @classmethod
    def _monitor_consumers(cls):
        """Monitor and restart failed consumers"""
        while not cls._stop_runner.is_set():
            for name, consumer in cls._consumers.items():
                if not consumer._consumer_thread.is_alive():
                    logger.warning(f"Consumer '{name}' died, restarting...")
                    consumer.start()
            sleep(5)


class EnableRabbitMQConsumerRegistry:
    """
    Enable and configure RabbitMQ consumers
    """
    def __init__(self):
        logger.info("Enabling RabbitMQ Consumer Registry")

    def enable(self):
        """Enable the consumer registry and start all consumers"""
        
        # users consumer
        RabbitMQConsumerRegistry.register_consumer(
            AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.name,
            connection_manager=RabbitMQConnectionManagerSingleton.get_instance(),
            queue=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.queue,
            exchange=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.exchange,
            routing_key=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.routing_binding_key,  # Use the first binding key for simplicity
            callback=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.callback,
            durable=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_USERS_CONSUMER.durable
        )
        
        # chats consumer
        RabbitMQConsumerRegistry.register_consumer(
            AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.name,
            connection_manager=RabbitMQConnectionManagerSingleton.get_instance(),
            queue=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.queue,
            exchange=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.exchange,
            routing_key=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.routing_binding_key,  # Use the first binding key for simplicity
            callback=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.callback,
            durable=AvailableConsumers.DOWNLY_EVENT_STATS_UPDATER_CHATS_CONSUMER.durable
        )
        
        
        RabbitMQConsumerRegistry.start_runner()
    
    def disable(self):
        """Disable the consumer registry and stop all consumers"""
        RabbitMQConsumerRegistry.stop_all()
        RabbitMQConsumerRegistry.stop_runner()
        logger.info("RabbitMQ Consumer Registry disabled")
        RabbitMQConsumerRegistry._consumers.clear()
        

class EnableRabbitMQPublisherRegistry:
    """
    Enable RabbitMQ Publisher Registry by registering the Downly Event Stats Updater Publisher.
    """
    def __init__(self):
        logger.info("Enabling RabbitMQ Publisher Registry for Downly Event Stats Updater Publisher")

    def enable(self):
        """
        Enable the RabbitMQ Publisher Registry.
        """
        
        RabbitMQConnectionManagerSingleton.get_instance().wait_for_connection()
        
        # Register the Downly Event Stats Updater Publisher
        RabbitMQPublisherRegistry.register_publisher(
            name=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.name,
            publisher=RabbitMQPublisher(
                connection_manager=RabbitMQConnectionManagerSingleton.get_instance(),
                exchange=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.exchange,
                exchange_type=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.exchange_type,
                durable=AvailablePublishers.DOWNLY_EVENT_STATS_UPDATER_PUBLISHER.durable
            )
        )