from enum import Enum

class EngineExchanges(Enum, str):
    """
    Enum for downly bot to engine exchanges.
    Each exchange represents a different service that the bot can interact with.
    supported services include gallery-dl, youtube-dl, and aria2.
    Each exchange is a string that represents the path to the worker module for that service.
    This allows the bot to send messages to the appropriate worker for processing.
    """
    GALLERY_DL = "downly.worker.gallery-dl.exchange"
    YOUTUBE_DL = "downly.worker.youtube-dl.exchange"
    ARIA2 = "downly.worker.aria2.exchange"

class EngineQueues(Enum, str):
    """
    Enum for downly bot to engine queues.
    """
    GALLERY_DL = "downly.worker.gallery-dl.queue"
    YOUTUBE_DL = "downly.worker.youtube-dl.queue"
    ARIA2 = "downly.worker.aria2.queue"