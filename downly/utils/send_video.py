from pyrogram.types import Message
from downly import get_logger
from downly.database.downloads_sql import add_download

logger = get_logger(__name__)


def get_extension(file_path: str):
    try:
        ext = f'.{file_path.split("?")[0].split(".")[-1]}'
        return ext
    except Exception as e:
        logger.error(f"Error getting extension: {e}")
        ext = None


def get_media_type(file_path: str):
    videos = [".mp4", ".mkv", ".webm", ".avi", ".flv", ".mov", ".wmv", ".m4v"]
    audios = [".mp3", ".m4a", ".flac", ".wav", ".ogg", ".opus"]
    images = [".jpg", ".jpeg", ".png", ".gif", ".webp"]

    ext = get_extension(file_path)
    logger.info(f"file extension: {ext}")

    if ext in videos:
        return "video"
    elif ext in audios:
        return "audio"
    elif ext in images:
        return "image"
    else:
        return "video"  # default to video if not in the above lists, because it's the most common


async def send_video(message: Message, video: str, progress=None):
    # check if the video is a file or a url
    media_type = get_media_type(video)
    if media_type == "video":
        replied_message = await message.reply_video(
            video=video, supports_streaming=True, progress=progress, quote=True
        )
    elif media_type == "audio":
        replied_message = await message.reply_audio(
            audio=video, progress=progress, quote=True
        )
    elif media_type == "image":
        replied_message = await message.reply_photo(photo=video, quote=True)
    else:
        replied_message = await message.reply_document(
            document=video, progress=progress, quote=True
        )

    # add download to database
    add_download(message.text, message.from_user.id, str(message.chat.id))
    return replied_message
