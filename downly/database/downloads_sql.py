import threading
from sqlalchemy import (
    Column,
    BigInteger,
    UnicodeText,
    String,
    Integer
)
from downly import get_logger
from downly.database import BASE, SESSION

logger = get_logger(__name__)


class Downloads(BASE):
    __tablename__ = "downloads"

    id = Column(Integer, primary_key=True, autoincrement=True)
    link = Column(UnicodeText, nullable=False)
    user_id = Column(BigInteger, nullable=False)
    chat_id = Column(String, nullable=True)

    def __init__(self, link: str, user_id: bin, chat_id: str):
        self.user_id = user_id
        self.chat_id = str(chat_id)  # ensure that chat_id is string
        self.link = link

    def __repr__(self):
        return "<Download %s>" % self.link


Downloads.__table__.create(bind=BASE.metadata.bind, checkfirst=True)

DOWNLOADS_INSERTION_LOCK = threading.RLock()


def add_download(link: str, user_id: bin, chat_id: str):
    with DOWNLOADS_INSERTION_LOCK:
        download = Downloads(link, user_id, chat_id)
        logger.info(f'[DB]: adding new download to db {link} ({user_id})')
        SESSION.add(download)
        SESSION.flush()
        SESSION.commit()


def count_downloads():
    try:
        return SESSION.query(Downloads).count()
    finally:
        SESSION.close()