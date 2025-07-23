import yaml
from functools import cache
from pydantic import BaseModel, Field
from typing import Optional


class TelegramConfig(BaseModel):
    """
    Configuration for Telegram API.
    Contains fields for API ID, API Hash, and Bot Token.
    """

    api_id: str = Field(default="")
    api_hash: str = Field(default="")
    bot_token: str = Field(default="")


class Configs(BaseModel):
    """
    Configuration for various plugins and settings.
    Contains fields for host, port, username, password, owner, channel ID,
    public status, and Cobalt base URL.
    """

    owner: str = Field(default="")
    channel_id: Optional[str] = Field(default=None)
    public: Optional[bool] = Field(default=False)


class DatabaseConfig(BaseModel):
    """
    Configuration for database connection.
    Contains fields for Redis URL and PostgreSQL URL.
    """

    postgres_url: str = Field(
        default="postgresql://downly:downly@localhost:5432/downly"
    )


class RabbitMQConfig(BaseModel):
    """
    Configuration for RabbitMQ connection.
    Contains fields for host, port, username, and password.
    """

    host: str = Field(default="localhost")
    port: int = Field(default=5672)
    username: str = Field(default="downly")
    password: str = Field(default="downly")

    # Additional fields for RabbitMQ configuration
    durable: bool = Field(default=True)


class DownlyConfig(BaseModel):
    """
    Main configuration class for Downly application.
    Contains fields for application name, Telegram configuration,
    plugin configurations, and database configuration.
    """

    app_name: str = Field(default="downly")
    telegram: TelegramConfig = Field(default=TelegramConfig())
    configs: Configs = Field(default=Configs())
    database: DatabaseConfig = Field(default=DatabaseConfig())
    rabbitmq: RabbitMQConfig = Field(default=RabbitMQConfig())


def load_config_from_yaml(file_path: str) -> DownlyConfig:
    """
    Load Downly configuration from a YAML file.
    Args:
        file_path (str): Path to the YAML configuration file.

    Returns:
        DownlyConfig: The loaded Downly configuration.
    """
    with open(file_path, "r") as file:
        data = yaml.safe_load(file)
    return DownlyConfig(**data.get("downly", {}))


class FailedToLoadConfig(Exception):
    pass