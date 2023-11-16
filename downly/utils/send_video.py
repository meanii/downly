from pyrogram.types import Message
from downly.database.downloads_sql import add_download


async def send_video(message: Message, video: str, progress=None):
    replied_message = await message.reply_video(
        video=video,
        supports_streaming=True,
        progress=progress,
        quote=True)

    # add download to database
    add_download(message.text, message.from_user.id, str(message.chat.id))
    return replied_message
