import threading

from sqlalchemy import (
    Column,
    BigInteger,
    UnicodeText,
    String
)
from downly import get_logger
from downly.database import BASE, SESSION

logger = get_logger(__name__)


class Users(BASE):
    __tablename__ = "users"

    user_id = Column(BigInteger, primary_key=True)
    username = Column(UnicodeText)

    def __init__(self, user_id, username=None):
        self.user_id = user_id
        self.username = username

    def __repr__(self):
        return "<User {} ({})>".format(self.username, self.user_id)


class Chats(BASE):
    __tablename__ = 'chats'
    chat_id = Column(String, primary_key=True)
    chat_name = Column(UnicodeText, nullable=False)

    def __init__(self, chat_id, chat_name):
        self.chat_id = str(chat_id)
        self.chat_name = chat_name

    def __repr__(self):
        return "<Chat {} ({})>".format(self.chat_name, self.chat_id)


Users.__table__.create(bind=BASE.metadata.bind, checkfirst=True)
Chats.__table__.create(bind=BASE.metadata.bind, checkfirst=True)

INSERTION_LOCK = threading.RLock()


def update_user(user_id: int, username: str):
    with INSERTION_LOCK:
        user = SESSION.query(Users).get(user_id)
        if not user:
            user = Users(user_id, username)
            logger.info(f'[DB]: adding new user to db {user_id} ({username})')
            SESSION.add(user)
            SESSION.flush()
        else:
            user.username = username

        SESSION.commit()


def update_chat(chat_id: str, chat_name: str):
    with INSERTION_LOCK:
        chat = SESSION.query(Chats).get(str(chat_id))
        if not chat:
            chat = Chats(chat_id, chat_name)
            logger.info(f'[DB]: adding new chat to db {chat_id} ({chat_name})')
            SESSION.add(chat)
            SESSION.flush()
        else:
            chat.chat_name = chat_name

        SESSION.commit()


def count_users():
    try:
        return SESSION.query(Users).count()
    finally:
        SESSION.close()


def count_chats():
    try:
        return SESSION.query(Chats).count()
    finally:
        SESSION.close()