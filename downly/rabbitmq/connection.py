import sys
import pika
import time
import random
import json
from loguru import logger
from downly.models.config import DownlyConfig
from typing import Optional, Union, Any, Dict


class RabbitMQConnection:
    """
    Represents a robust connection to a RabbitMQ server with automatic reconnection.
    Usage:
        with RabbitMQConnection(host="localhost", port=5672, username="guest", password="guest") as conn:
            conn.declare_exchange("my_exchange", "direct")
            conn.declare_queue("my_queue")
            conn.publish("message")
    """

    def __init__(
        self, 
        *, 
        host: str, 
        port: int, 
        username: str, 
        password: str,
        max_reconnect_attempts: int = 5,
        reconnect_backoff_base: float = 1.0,
        reconnect_backoff_max: float = 30.0
    ):
        self.host = host
        self.port = port
        self.username = username
        self.password = password
        self.connection: Optional[pika.BlockingConnection] = None
        self.channel: Optional[pika.adapters.blocking_connection.BlockingChannel] = None
        logger.debug(
            f"RabbitMQConnection initialized with host={host}, port={port}, username={username}"
        )

        # Exchange and queue parameters storage
        self.exchange_name: Optional[str] = None
        self.exchange_type: Optional[str] = None
        self.exchange_durable: Optional[bool] = None
        self.queue_name: Optional[str] = None
        self.queue_durable: Optional[bool] = None
        
        # Reconnection parameters
        self._max_reconnect_attempts = max_reconnect_attempts
        self._reconnect_backoff_base = reconnect_backoff_base
        self._reconnect_backoff_max = reconnect_backoff_max
        self._reconnect_attempts = 0
        self._reconnecting = False

    def get_connection_string(self) -> str:
        return f"amqp://{self.username}:{self.password}@{self.host}:{self.port}"

    def connect(self) -> None:
        """Establishes a new connection to RabbitMQ with error handling."""
        try:
            # Clean up previous connection if exists
            if self.connection and not self.connection.is_closed:
                try:
                    self.connection.close()
                except Exception as e:
                    logger.warning(f"Error closing previous connection: {e}")
            
            parameters = pika.ConnectionParameters(
                host=self.host,
                port=self.port,
                credentials=pika.PlainCredentials(self.username, self.password),
                heartbeat=600,
                blocked_connection_timeout=300,
            )
            logger.debug(f"Creating RabbitMQ connection with parameters: {parameters}")
            self.connection = pika.BlockingConnection(parameters)
            self.channel = self.connection.channel()
            self._reconnect_attempts = 0  # Reset on successful connection
            logger.info("[RabbitMQ] Connection established successfully")
            
            # Re-declare exchange and queue if they were previously set
            if self.exchange_name:
                self._declare_exchange_internal()
            if self.queue_name:
                self._declare_queue_internal()
                
        except Exception as e:
            logger.error(f"Failed to connect to RabbitMQ: {e}")
            self.connection = None
            self.channel = None
            raise

    def _get_backoff(self) -> float:
        """Calculate exponential backoff with jitter."""
        backoff = min(
            self._reconnect_backoff_base * (2 ** self._reconnect_attempts),
            self._reconnect_backoff_max
        )
        jitter = random.uniform(0, backoff * 0.3)  # Up to 30% jitter
        return backoff + jitter

    def _ensure_connection(self) -> None:
        """Ensure we have a valid connection with robust reconnection handling."""
        if self._reconnecting:
            logger.debug("Reconnection already in progress")
            return
            
        try:
            # Check if connection is usable
            if self.connection and not self.connection.is_closed:
                # Verify channel status
                if self.channel and not self.channel.is_closed:
                    return
                try:
                    self.channel = self.connection.channel()
                    return
                except Exception as e:
                    logger.warning(f"Channel creation failed: {e}")
        except Exception as e:
            logger.debug(f"Connection check failed: {e}")

        # Start reconnection process
        self._reconnecting = True
        self._reconnect_attempts = 0
        logger.warning("RabbitMQ connection is down. Attempting to reconnect...")
        
        while self._reconnect_attempts < self._max_reconnect_attempts:
            try:
                self.connect()
                self._reconnecting = False
                return
            except Exception as e:
                self._reconnect_attempts += 1
                if self._reconnect_attempts >= self._max_reconnect_attempts:
                    logger.critical(f"Max reconnection attempts ({self._max_reconnect_attempts}) reached")
                    self._reconnecting = False
                    raise ConnectionError("Failed to establish RabbitMQ connection") from e
                
                backoff = self._get_backoff()
                logger.warning(f"Reconnect attempt {self._reconnect_attempts} failed. Retrying in {backoff:.1f}s: {e}")
                time.sleep(backoff)

    def _declare_exchange_internal(self) -> None:
        """Internal exchange declaration without connection checks."""
        logger.info(f"Declaring exchange '{self.exchange_name}' (type={self.exchange_type}, durable={self.exchange_durable})")
        try:
            # First check if exchange exists with passive declare
            self.channel.exchange_declare(
                exchange=self.exchange_name,
                exchange_type=self.exchange_type,
                passive=True  # Just check existence
            )
            logger.debug(f"Exchange '{self.exchange_name}' already exists")
        except pika.exceptions.ChannelClosedByBroker as e:
            if e.reply_code == 404:  # Not found
                logger.debug(f"Exchange '{self.exchange_name}' doesn't exist, creating it")
                self.channel.exchange_declare(
                    exchange=self.exchange_name,
                    exchange_type=self.exchange_type,
                    durable=self.exchange_durable
                )
            else:
                raise

    def declare_exchange(self, exchange_name: str, exchange_type: str = "direct", durable: bool = True) -> None:
        """
        Declares a RabbitMQ exchange with the specified name and type.
        Automatically handles reconnection if needed.
        """
        # Store parameters for re-declaration after reconnection
        self.exchange_name = exchange_name
        self.exchange_type = exchange_type
        self.exchange_durable = durable
        
        self._ensure_connection()
        self._declare_exchange_internal()

    def _declare_queue_internal(self) -> None:
        """Internal queue declaration without connection checks."""
        logger.info(f"Declaring queue '{self.queue_name}' (durable={self.queue_durable})")
        try:
            # First check if queue exists with passive declare
            self.channel.queue_declare(
                queue=self.queue_name,
                passive=True  # Just check existence
            )
            logger.debug(f"Queue '{self.queue_name}' already exists")
        except pika.exceptions.ChannelClosedByBroker as e:
            if e.reply_code == 404:  # Not found
                logger.debug(f"Queue '{self.queue_name}' doesn't exist, creating it")
                self.channel.queue_declare(
                    queue=self.queue_name,
                    durable=self.queue_durable
                )
            else:
                raise
        
        # Bind queue if exchange is declared
        if self.exchange_name:
            logger.debug(f"Binding queue '{self.queue_name}' to exchange '{self.exchange_name}'")
            self.channel.queue_bind(
                exchange=self.exchange_name,
                queue=self.queue_name,
                routing_key=self.queue_name
            )

    def declare_queue(self, queue_name: str, durable: bool = True) -> None:
        """
        Declares a RabbitMQ queue with the specified name.
        Automatically handles reconnection if needed.
        """
        # Store parameters for re-declaration after reconnection
        self.queue_name = queue_name
        self.queue_durable = durable
        
        self._ensure_connection()
        self._declare_queue_internal()

    def publish(self, message: Union[str, bytes, Dict[str, Any]]) -> None:
        """
        Publishes a message with automatic serialization and connection recovery.
        Supports strings, bytes, and dictionaries (auto-converted to JSON).
        """
        if not self.exchange_name or not self.queue_name:
            raise RuntimeError("Exchange and queue must be declared before publishing")

        # Serialize message to bytes
        if isinstance(message, dict):
            body = json.dumps(message).encode('utf-8')
            content_type = 'application/json'
        elif isinstance(message, str):
            body = message.encode('utf-8')
            content_type = 'text/plain'
        elif isinstance(message, bytes):
            body = message
            content_type = 'application/octet-stream'
        else:
            raise TypeError(f"Unsupported message type: {type(message)}")

        self._ensure_connection()
        
        try:
            self.channel.basic_publish(
                exchange=self.exchange_name,
                routing_key=self.queue_name,
                body=body,
                properties=pika.BasicProperties(
                    delivery_mode=2,  # Make message persistent
                    content_type=content_type
                )
            )
            logger.debug(f"Published message to {self.exchange_name}/{self.queue_name}")
        except Exception as e:
            logger.error(f"Publish failed: {e}")
            # Mark connection as broken for next attempt
            self.connection = None
            self.channel = None
            raise

    def close(self) -> None:
        """Close the connection safely."""
        if self.connection and not self.connection.is_closed:
            logger.debug("Closing RabbitMQ connection")
            try:
                self.connection.close()
            except Exception as e:
                logger.warning(f"Error closing connection: {e}")
            finally:
                self.connection = None
                self.channel = None

    def __enter__(self):
        self.connect()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()



def initialize_rabbitmq_client(config: DownlyConfig) -> RabbitMQConnection:
    """
    Initialize and configure RabbitMQ client with robust error handling.
    
    Args:
        config: Application configuration object
        
    Returns:
        Configured RabbitMQConnection instance
        
    Raises:
        SystemExit: If unable to establish RabbitMQ connection
    """
    try:
        client = RabbitMQConnection(
            host=config.rabbitmq.host,
            port=config.rabbitmq.port,
            username=config.rabbitmq.username,
            password=config.rabbitmq.password,
            max_reconnect_attempts=5,
            reconnect_backoff_base=1.0,
            reconnect_backoff_max=30.0
        )
        
        # Use context manager for safe connection handling
        with client:
            # Declare exchange and queue
            client.declare_exchange(
                exchange_name=config.rabbitmq.exchange,
                exchange_type="direct",
                durable=config.rabbitmq.durable
            )
            client.declare_queue(
                queue_name=config.rabbitmq.queue,
                durable=config.rabbitmq.durable
            )
            # Binding is handled internally by declare_queue when exchange exists
            
        logger.info("[RabbitMQ] Connection established and configured successfully")
        return client
        
    except Exception as e:
        logger.critical(f"[RabbitMQ] Failed to initialize RabbitMQ connection: {e}")
        logger.exception("RabbitMQ initialization error details")
        # Exit with non-zero status for process managers
        sys.exit(1)