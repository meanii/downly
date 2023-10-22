import time

from pyrogram import filters, Client
from pyrogram.types import Message
from downly.downly import Downly
from downly import get_logger
from downly.engine.cobalt import CobaltEngine
from downly.utils.validator import validate_url, is_supported_service
from downly.utils.b_logger import b_logger
from downly.handlers.stream_downloader import StreamDownloader
from downly.handlers.youtube_downloader import YoutubeDownloader
from pathlib import Path

logger = get_logger(__name__)


@Downly.on_message(filters.private | filters.group, group=1)
@b_logger
async def download(client: Client, message: Message):
    # check if message is command then do nothing
    if message.command:
        return

    if not message.text:
        return

    user_url_message = message.text

    # check if user is from available service
    if not is_supported_service(user_url_message):
        logger.warning(f'unsupported service {user_url_message}')
        return

    # validating valid url by urllib
    if not validate_url(user_url_message):
        logger.warning(f'invalid url {user_url_message}')
        return

    first_message = await message.reply_text('processing your request...', quote=True)

    try:
        output = CobaltEngine().download({
            'url': user_url_message,
        })
    except Exception as e:
        logger.error(f'Error while processing {user_url_message}\n'
                     f'error message: {e}')
        return await first_message.edit_text('Error!, please try again later\n'
                                             f'message: `{e}`')

    # logging output
    logger.info(f'handing request for {user_url_message} with output {output} - '
                f'from {message.from_user.first_name}({message.from_user.id})')

    # handling output
    # handling stream, expect YouTube because of slow download
    if output.get('status') == 'stream':

        output_dir = Path.resolve(
            Path.cwd() / 'downloads' / 'stream' / f'{message.from_user.id}' / f'{time.time():.0f}')

        # handle YouTube stream

        if 'youtube' in user_url_message:
            downloader = YoutubeDownloader(
                youtube_url=user_url_message,
                output_dir=output_dir
            )
        else:
            # handling stream
            downloader = StreamDownloader(
                url=output.get('url'),
                output_path=str(
                    Path.resolve(output_dir / '[STREAM_FILENAME]'))
            )

        # downloading stream
        try:
            downloaded_file = await downloader.download()
        except Exception as e:
            logger.error(f'Error while downloading stream for {user_url_message} - '
                         f'error message: {e}')
            return await first_message.edit_text('Error!, please try again later\n'
                                                 f'message: `{e}`')

        # progress callback
        async def progress(current, total):
            logger.info(
                f'uploading for {message.from_user.first_name}({message.from_user.id}) '
                f'{current * 100 / total:.1f}% '
                f'input: {user_url_message}'
            )

        # sending video
        await message.reply_video(
            video=downloaded_file,
            supports_streaming=True,
            progress=progress,
            quote=True)

        # delete downloaded file
        await downloader.delete()
        await first_message.delete()
        return

    if output.get('status') == 'redirect':
        await message.reply_video(video=output.get("url"), quote=True)
        await first_message.delete()
        logger.info(f'finished handling request for {user_url_message} - '
                    f'from {message.from_user.first_name}({message.from_user.id})')
        return

    if output.get('status') == 'error':
        return first_message.edit_text('Error!, please try again later'
                                       f'message: `{output.get("text")}`')

    await first_message.edit_text('Error!, please try again later')
