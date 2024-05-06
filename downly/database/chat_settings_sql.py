import threading
import datetime

from downly import get_logger
from downly.database import BASE, SESSION

from sqlalchemy import Column, BigInteger, UnicodeText, String, DateTime, Boolean

logger = get_logger(__name__)

class ChatSettings(BASE):
    
    __tablename__ = "chat_settings"
    
    chat_id = Column(BigInteger, primary_key=True)
    dd_mode = Column(Boolean, default=True)
    created_at = Column(DateTime, default=datetime.datetime.now)
    updated_at = Column(
        DateTime, default=datetime.datetime.now, onupdate=datetime.datetime.now
    )
    
    def __init__(self, chat_id: int, dd_mode: bool = True):
        self.chat_id = chat_id
        self.dd_mode = dd_mode
    
    def __repr__(self):
        return "<ChatSettings {} ({})>".format(self.chat_id, self.dd_mode)


ChatSettings.__table__.create(bind=BASE.metadata.bind, checkfirst=True)

INSERTION_LOCK = threading.RLock()


def dd_toggle(chat_id: int, dd_mode: bool) -> None:
    """dd toggle enable or disable chat's ddinstagram, ddyoutube

    Args:
        chat_id (int): chat id or user id
        dd_mode (bool): True or False, for enabling and disabling
    """
    with INSERTION_LOCK:
        chat_settings = ChatSettings(chat_id=chat_id, dd_mode=dd_mode)
        logger.info(f'[{__file__}]: dd toggle changing for {chat_id} to {dd_mode}')
        SESSION.add(chat_settings)
        SESSION.flush()
        SESSION.commit()

def is_dd_mode_enabled(chat_id: int) -> bool:
    """get if dd mode is enabled for a chat_id or not

    Args:
        chat_id (int): pass the chat id or user id

    Returns:
        bool: returning, if dd mode is enabled, if chat id doesnt exists
        return True by default
    """
    try:
        chat_settings = SESSION.query(ChatSettings).get(chat_id)
        if not chat_settings:
            return True
        return chat_settings.dd_mode
    finally:
        SESSION.close()