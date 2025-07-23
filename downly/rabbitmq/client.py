import threading
from time import sleep
from typing import Optional
import pika

from threading import Condition, RLock
from pika.adapters.blocking_connection import BlockingConnection, BlockingChannel
from pika.exceptions import AMQPConnectionError
from pydantic import BaseModel
from loguru import logger


class RabbitMQConnectionConfig(BaseModel):
    """
    Configuration for RabbitMQ connection.
    """

    host: str = "localhost"
    port: int = 5672
    username: str = "downly"
    password: str = "downly"
    virtual_host: str = "/"

    heartbeat_interval: int = 5  # use for connection heartbeat
    retry_delay: int = 5  # delay between retry attempts in seconds

    daemon_check_interval: int = (
        5  # interval for the daemon thread to check connection status
    )

    def get_connection_params(self) -> pika.ConnectionParameters:
        """
        Returns a pika.ConnectionParameters object based on the configuration.
        """
        return pika.ConnectionParameters(
            host=self.host,
            port=self.port,
            virtual_host=self.virtual_host,
            heartbeat=self.heartbeat_interval,
            retry_delay=self.retry_delay,
            credentials=pika.PlainCredentials(self.username, self.password),
        )


class RabbitMQConnectionManager:
    """
    Manages RabbitMQ connection with automatic reconnection.
    This class ensures that the connection to RabbitMQ is established and maintained.
    Example usage:
    ```python
    from rc import RabbitMQConnectionManager, RabbitMQConnectionConfig
    config = RabbitMQConnectionConfig(
        host="localhost",
        port=5672,
        username="downly",
        password="downly",
        virtual_host="/"
    )
    connection_manager = RabbitMQConnectionManager(config)
    connection_manager.ensure_connection() # Ensures the connection is established
    ```
    """

    def __init__(self, config: RabbitMQConnectionConfig):
        self.config = config

        self.connection: Optional[BlockingConnection] = None
        self.channel: Optional[BlockingChannel] = None

        self.lock = RLock()
        self.condition = Condition(self.lock)

        # Connection management
        self._stop_event = threading.Event()  # Event to control thread exit
        self.deamon_thread = None
        self._enable_deamon_connection_repair()

    def connect(self):
        """
        Establishes a connection to RabbitMQ.
        """
        with self.lock:
            if self.connection and self.connection.is_open:
                return

            try:
                self.connection = pika.BlockingConnection(
                    self.config.get_connection_params()
                )
                self.channel = self.connection.channel()
                logger.info("Connected to RabbitMQ")
            except AMQPConnectionError as e:
                logger.error(f"Failed to connect to RabbitMQ: {e}")
                raise

    def ping(self) -> bool:
        """
        Pings the RabbitMQ server to check if the connection is alive.
        If the connection or channel is not alive, it will attempt to reconnect.
        """
        with self.lock:
            # Check both connection and channel status
            if not (self.connection and self.connection.is_open) or not (
                self.channel and self.channel.is_open
            ):
                logger.warning(
                    "Connection or channel is not open, attempting to reconnect..."
                )
                return False
            else:
                try:
                    self.connection.process_data_events()
                    return True
                except Exception as e:  # Catch broader exceptions
                    logger.error(f"Ping failed: {e}")
                    return False

    def _retry_connection(self):
        """
        Retries the connection to RabbitMQ with exponential backoff.
        """
        with self.lock:
            while not self.ping():
                if self._stop_event.is_set():
                    logger.info("Connection retry stopped due to stop event")
                    return

                try:
                    self.connect()
                    return
                except AMQPConnectionError:
                    logger.warning(
                        "Retrying RabbitMQ connection in {} seconds...".format(
                            self.config.retry_delay
                        )
                    )
                    self.condition.wait(timeout=self.config.retry_delay)

    def ensure_connection(self):
        """
        Ensures that the connection to RabbitMQ is established.
        If the connection is lost, it will retry to connect.
        """
        with self.lock:
            if not self.ping():
                self._retry_connection()
        
    def wait_for_connection(self):
        """
        Waits for the connection to RabbitMQ to be established.
        This method blocks until the connection is available.
        """
        with self.lock:
            while not self.ping():
                logger.info("Waiting for RabbitMQ connection...")
                self.condition.wait(timeout=self.config.retry_delay)
                self.ensure_connection()

    def _enable_deamon_connection_repair(self):
        """
        Enables a daemon thread to automatically repair the RabbitMQ connection.
        This thread will run in the background and attempt to reconnect if the connection is lost.
        """
        if self.deamon_thread is not None:
            logger.warning("Daemon connection repair thread is already running")
            return

        def repair_connection():
            while not self._stop_event.is_set():

                if not self.ping():
                    logger.info("Attempting to repair RabbitMQ connection...")
                    self._retry_connection()
                    logger.info("RabbitMQ connection repair attempt finished")
                else:
                    logger.debug("RabbitMQ connection is healthy")

                # Wait for the next check interval
                sleep(self.config.daemon_check_interval)

        self.deamon_thread = threading.Thread(target=repair_connection, daemon=True)
        self.deamon_thread.start()
        logger.info("Daemon connection repair thread started")

    def close(self):
        """
        Closes the RabbitMQ connection and channel.
        """

        # Signal the daemon thread to stop
        self._stop_event.set()

        with self.lock:
            # Wake up the thread if it is waiting on the condition
            # This allows it to check the stop event and exit promptly
            self.condition.notify_all()

            # Stop the daemon thread first
            if self.deamon_thread and self.deamon_thread.is_alive():
                self.deamon_thread.join(timeout=2)  # Wait for it to finish
                self.deamon_thread = None

            # Close the channel if it is open
            if self.channel and self.channel.is_open:
                self.channel.close()

            # Close the connection if it is open
            if self.connection and self.connection.is_open:
                self.connection.close()

            logger.info("RabbitMQ connection closed")



class RabbitMQConnectionManagerSingleton:
    """
    Singleton class to manage RabbitMQ connection.
    Ensures that only one instance of RabbitMQConnectionManager exists.
    """

    _instance: Optional[RabbitMQConnectionManager] = None

    @classmethod
    def get_instance(
        cls, config: Optional[RabbitMQConnectionConfig] = None
    ) -> RabbitMQConnectionManager:
        if cls._instance is None:
            if config is None:
                raise ValueError(
                    "Configuration must be provided for the first instance."
                )
            cls._instance = RabbitMQConnectionManager(config)
        return cls._instance