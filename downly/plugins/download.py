import time

from pathlib import Path
from urllib.parse import urlparse

from pyrogram import filters, Client
from pyrogram.types import Message

from downly import get_logger
from downly.downly import Downly

from downly.engine.cobalt import CobaltEngine

from downly.utils.validator import validate_url, is_supported_service
from downly.utils.b_logger import b_logger
from downly.utils.message import get_chat_info
from downly.utils.send_video import send_video
from downly.utils.progress import Progress

from downly.handlers.stream_downloader import StreamDownloader
from downly.handlers.youtube_downloader import YoutubeDownloader

logger = get_logger(__name__)


@Downly.on_message(filters.private | filters.group | filters.channel, group=1)
@b_logger
async def download(client: Client, message: Message):
    # check if a message is command then do nothing
    if message.command:
        return

    if not message.text:
        return

    user_url_message = message.text

    # get chat info if a message is from a group or channel
    title, id = get_chat_info(message)

    # validating valid url by urllib
    if not validate_url(user_url_message):
        return

    # check if user is from available service
    if not is_supported_service(user_url_message):
        logger.warning(f'unsupported service {user_url_message}')
        return

    first_message = await message.reply_text('processing your request...', quote=True)
    domain = urlparse(user_url_message).hostname.replace('www.', '')

    # handling YouTube first
    output_dir = Path.resolve(
        Path.cwd() / 'downloads' / 'stream' / f'{id}' / f'{time.time():.0f}')

    async def download_stream(user_message: Message, downloader_instance):
        """ Download stream from YouTube and Cobalt Engine """
        # downloading stream
        try:
            user_message = await user_message.edit_text('downloading stream...')
            downloaded_file = await downloader_instance.download()
        except Exception as e:
            error = (
                'Oops! Something went wrong.\n'
                'Please try again later.\n'
                f'Details: `{e}`'
            )
            logger.error(error)
            return await user_message.edit_text(error)

        # progress callback
        progress = Progress(message=user_message)

        # sending video
        await send_video(message=message, video=downloaded_file, progress=progress.progress)

        # delete downloaded file
        await downloader.delete()
        await user_message.delete()
        return

    # handle YouTube stream
    if domain in ['youtube.com', 'youtu.be']:
        downloader = YoutubeDownloader(
            youtube_url=user_url_message,
            output_dir=output_dir
        )
        await download_stream(first_message, downloader)
        return

    try:
        output = CobaltEngine().download({
            'url': user_url_message,
        })
    except Exception as e:
        logger.error(f'Error occurred while processing {user_url_message}\n'
                     f'Error message: {e}')

        error_message = (
            'Oops! Something went wrong.\n'
            'Please try again later.\n'
            f'Details: `{e}`'
        )
        return await first_message.edit_text(error_message)

    # logging output
    logger.info(f'handing request for {user_url_message} with output {output} - '
                f'from {title}({id})')

    # handling output

    # handling error message
    if output.get('status') == 'error':
        error_message = (
            'Apologies, an error occurred.\n'
            'Please attempt your request later.\n'
            f'Message details: `{output.get("text")}`'
        )
        return await first_message.edit_text(error_message)

    # handling stream, expect YouTube because of slow download
    if output.get('status') == 'stream':
        # handling stream
        downloader = StreamDownloader(
            url=output.get('url'),
            output_path=str(
                Path.resolve(output_dir / '[STREAM_FILENAME]'))
        )

        # downloading stream
        await download_stream(first_message, downloader)
        return

    if output.get('status') == 'redirect':
        # sending video
        await send_video(message=message, video=output.get("url"))

        await first_message.delete()
        logger.info(f'finished handling request for {user_url_message} - '
                    f'from {title}({id})')
        return

    if output.get('status') == 'picker':
        pickers = output.get('picker')
        if not pickers:
            return await first_message.edit_text('No video found.')

        for picker in pickers:
            await send_video(message=message, video=picker.get("url"))
        logger.info(f'finished handling request for {user_url_message} - '
                    f'from {title}({id})')
        return


    error_message = 'An error occurred. Please try again later.'
    return await first_message.edit_text(error_message)
