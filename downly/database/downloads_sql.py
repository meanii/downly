import threading
import datetime
from sqlalchemy import Column, BigInteger, UnicodeText, String, Integer, DateTime
from downly import get_logger
from downly.database import BASE, SESSION

logger = get_logger(__name__)


class Downloads(BASE):
    __tablename__ = "downloads"

    id = Column(Integer, primary_key=True, autoincrement=True)
    link = Column(UnicodeText, nullable=False)
    user_id = Column(BigInteger, nullable=False)
    chat_id = Column(String, nullable=True)
    created_at = Column(DateTime, default=datetime.datetime.now)

    def __init__(self, link: str, user_id: bin, chat_id: str):
        self.user_id = user_id
        self.chat_id = str(chat_id)  # ensure that chat_id is string
        self.link = link

    def __repr__(self):
        return "<Download %s>" % self.link


Downloads.__table__.create(bind=BASE.metadata.bind, checkfirst=True)

DOWNLOADS_INSERTION_LOCK = threading.RLock()


def add_download(link: str, user_id: bin, chat_id: str):
    """
    Add a new download to the db
    """
    with DOWNLOADS_INSERTION_LOCK:
        download = Downloads(link, user_id, chat_id)
        logger.info(f"[{__file__}]: adding new download to db {link} ({user_id})")
        SESSION.add(download)
        SESSION.flush()
        SESSION.commit()


def count_downloads():
    """
    count all downloads
    """
    try:
        return SESSION.query(Downloads).count()
    finally:
        SESSION.close()


def count_last_24_hours_downloads():
    """
    Count downloads in the last 24 hours
    """
    try:
        return (
            SESSION.query(Downloads)
            .filter(
                Downloads.created_at
                > (datetime.datetime.now() - datetime.timedelta(days=1))
            )
            .count()
        )
    finally:
        SESSION.close()

