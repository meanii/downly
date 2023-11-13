from dataclasses import dataclass


@dataclass
class BotInfo:
    username: str
    id: int


bot = BotInfo
