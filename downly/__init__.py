import sys
from loguru import logger

from downly.monitor import MonitoringTaskRunner

from downly.config import Config
from downly import database # loading database before rabbitmq, and just after config ( config auto load itself )

from downly.rabbitmq.client import RabbitMQConnectionConfig, RabbitMQConnectionManagerSingleton
from downly.rabbitmq.registry import EnableRabbitMQPublisherRegistry, EnableRabbitMQConsumerRegistry


__config__ = Config().get_instance()

# Initialize RabbitMQ client
rabbit_client = RabbitMQConnectionManagerSingleton.get_instance(
    config=RabbitMQConnectionConfig(
        host=__config__.rabbitmq.host,
        port=__config__.rabbitmq.port,
        username=__config__.rabbitmq.username,
        password=__config__.rabbitmq.password,
    )
)
rabbit_client.ensure_connection()

# Ensure the RabbitMQ connection is established
if not rabbit_client.ping():
    logger.critical("Failed to connect to RabbitMQ server")
    sys.exit(1)

# Enable RabbitMQ Publisher Registry
enable_rabbitmq_publisher_registry = EnableRabbitMQPublisherRegistry()
enable_rabbitmq_publisher_registry.enable()

# Enable RabbitMQ Consumer Registry
enable_rabbitmq_consumer_registry = EnableRabbitMQConsumerRegistry()
enable_rabbitmq_consumer_registry.enable()

# Start background monitoring - fire and forget
monitor = MonitoringTaskRunner()
monitor.start_background_monitor(interval=10)

# Your main application continues
logger.info("Background monitoring started, continuing with main application...")
