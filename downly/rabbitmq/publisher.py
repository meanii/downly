import json
from typing import Union
from loguru import logger

from downly.utils.fire_and_forget import fire_and_forget
from downly.clients.rabbitmq import RabbitMQConnectionManager


class RabbitMQPublisher:
    """
    Base class for RabbitMQ publishers.
    This class should be extended by specific publisher implementations.
    """

    def __init__(
        self,
        connection_manager: RabbitMQConnectionManager,
        *,
        exchange: str,
        exchange_type: str,
        durable: bool = True,
    ):
        self.connection_manager = connection_manager
        self.exchange = exchange
        self.exchange_type = exchange_type
        self.durable = durable

        # Ensure the exchange is set up
        self._setup_exchange()

    def _setup_exchange(self):
        """
        Sets up the exchange if it does not exist.
        This method can be overridden by subclasses to customize exchange setup.
        """
        with self.connection_manager.lock:
            self.connection_manager.ensure_connection()
            channel = self.connection_manager.channel

            if not channel or not channel.is_open:
                logger.error("Channel is not available for exchange declaration")
                return

            try:
                # check if the exchange already exists
                channel.exchange_declare(
                    exchange=self.exchange,
                    exchange_type=self.exchange_type,
                    durable=self.durable,
                    passive=False,  # passive=False means it will be created if it doesn't exist
                )
                logger.info(f"Exchange declared: {self.exchange}")
            except Exception as e:  # Catch all exceptions
                logger.error(f"Failed to declare exchange: {e}")

    @fire_and_forget
    def publish(self, routing_key: str, body: Union[str, bytes, dict]):
        with self.connection_manager.lock:
            self.connection_manager.ensure_connection()
            channel = self.connection_manager.channel

            if not channel or not channel.is_open:
                logger.error("Channel is not available for publishing")
                return

            try:
                logger.info(
                    f"Publishing message to exchange: {self.exchange}, routing key: {routing_key}, body: {body}"
                )
                if isinstance(body, str):
                    body = body.encode("utf-8")
                elif isinstance(body, dict):
                    body = json.dumps(body).encode("utf-8")

                channel.basic_publish(
                    exchange=self.exchange,
                    routing_key=routing_key,
                    body=body,
                )
                logger.info(
                    f"Message published to exchange: {self.exchange}, routing key: {routing_key}"
                )
            except Exception as e:  # Catch all exceptions
                logger.error(f"Failed to publish message: {e}")