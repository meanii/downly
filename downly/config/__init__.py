import sys
from pathlib import Path
from typing import Optional, Union
from downly.models.config import DownlyConfig, load_config_from_yaml
from loguru import logger



class ConfigLoad:
    def __init__(self, config_path: Optional[Path] = None) -> Union[DownlyConfig, None]:
        self.config: DownlyConfig
        self.config_path = config_path
        self._load()
        
    def get_config(self) -> DownlyConfig:
        return self.config
    
    def _load(self):
        try:
            self.config = load_config_from_yaml(self.config_path)
            logger.info(f"Loaded configuration from {self.config_path}")
        except Exception as e:
            logger.critical(f"Failed to load configuration: {e}")
            sys.exit(1)
            
class Config:
    """
    Singleton class to manage RabbitMQ connection.
    Ensures that only one instance of RabbitMQConnectionManager exists.
    """

    _instance: Optional[DownlyConfig] = None

    @classmethod
    def get_instance(
        cls, config_path: Optional[Path] = None
    ) -> DownlyConfig:
        if cls._instance is None:
            if config_path is None:
                raise ValueError(
                    "Configuration must be provided for the first instance."
                )
            cls._instance = ConfigLoad(config_path).get_config()
        return cls._instance
    
# auto load
config_path = Path.resolve(Path.cwd() / "config.yaml")
Config().get_instance(config_path)