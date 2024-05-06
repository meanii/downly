import threading
import datetime
from sqlalchemy import Column, BigInteger, UnicodeText, String, DateTime
from downly import get_logger
from downly.database import BASE, SESSION

logger = get_logger(__name__)


class Users(BASE):
    __tablename__ = "users"

    user_id = Column(BigInteger, primary_key=True)
    username = Column(UnicodeText)
    created_at = Column(DateTime, default=datetime.datetime.now)
    updated_at = Column(
        DateTime, default=datetime.datetime.now, onupdate=datetime.datetime.now
    )

    def __init__(self, user_id, username=None):
        self.user_id = user_id
        self.username = username

    def __repr__(self):
        return "<User {} ({})>".format(self.username, self.user_id)


class Chats(BASE):
    __tablename__ = "chats"
    chat_id = Column(String, primary_key=True)
    chat_name = Column(UnicodeText, nullable=False)
    created_at = Column(DateTime, default=datetime.datetime.now)
    updated_at = Column(
        DateTime, default=datetime.datetime.now, onupdate=datetime.datetime.now
    )

    def __init__(self, chat_id, chat_name):
        self.chat_id = str(chat_id)
        self.chat_name = chat_name

    def __repr__(self):
        return "<Chat {} ({})>".format(self.chat_name, self.chat_id)


Users.__table__.create(bind=BASE.metadata.bind, checkfirst=True)
Chats.__table__.create(bind=BASE.metadata.bind, checkfirst=True)

INSERTION_LOCK = threading.RLock()


def update_user(user_id: int, username: str):
    """
    Add/Update a user in the db
    """
    with INSERTION_LOCK:
        user = SESSION.query(Users).get(user_id)
        if not user:
            user = Users(user_id, username)
            logger.info(f"[{__file__}]: adding new user to db {user_id} ({username})")
            SESSION.add(user)
            SESSION.flush()
        else:
            user.username = username
            user.updated_at = datetime.datetime.now()

        SESSION.commit()


def update_chat(chat_id: str, chat_name: str):
    """
    Add/Update a chat in the db
    """
    with INSERTION_LOCK:
        chat = SESSION.query(Chats).get(str(chat_id))
        if not chat:
            chat = Chats(chat_id, chat_name)
            logger.info(f"[{__file__}]: adding new chat to db {chat_id} ({chat_name})")
            SESSION.add(chat)
            SESSION.flush()
        else:
            chat.chat_name = chat_name
            chat.updated_at = datetime.datetime.now()

        SESSION.commit()


def count_users():
    """
    Count the number of users in the database
    """
    try:
        return SESSION.query(Users).count()
    finally:
        SESSION.close()


def count_chats():
    """
    Count the number of chats in the database
    """
    try:
        return SESSION.query(Chats).count()
    finally:
        SESSION.close()


def count_last_24_hours_active_users():
    """
    Count users who have used the bot in the last 24 hours
    """
    try:
        return (
            SESSION.query(Users)
            .filter(
                Users.updated_at > datetime.datetime.now() - datetime.timedelta(days=1)
            )
            .count()
        )
    finally:
        SESSION.close()


def count_last_24_hours_users():
    """
    Count users who have joined in the last 24
    """
    try:
        return (
            SESSION.query(Users)
            .filter(
                Users.created_at > datetime.datetime.now() - datetime.timedelta(days=1)
            )
            .count()
        )
    finally:
        SESSION.close()


def count_last_24_hours_chats():
    """
    Count chats that have been active in the last 24 hours
    """
    try:
        return (
            SESSION.query(Chats)
            .filter(
                Chats.created_at > datetime.datetime.now() - datetime.timedelta(days=1)
            )
            .count()
        )
    finally:
        SESSION.close()


def count_last_24_hours_active_chats():
    """
    Count chats that have been active in the last 24 hours
    """
    try:
        return (
            SESSION.query(Chats)
            .filter(
                Chats.updated_at > datetime.datetime.now() - datetime.timedelta(days=1)
            )
            .count()
        )
    finally:
        SESSION.close()
