import time

from pyrogram import filters, Client
from pyrogram.types import Message
from downly.downly import Downly
from downly.engine.cobalt import CobaltEngine
from downly.utils.validator import validate_url
from downly.engine.stream_downloader import StreamDownloader
from pathlib import Path

@Downly.on_message(filters.private)
async def download(client: Client, message: Message):
    user_url_message = message.text

    # validating valid url by urllib
    if not validate_url(user_url_message):
        await message.reply_text('Invalid url!\nplease try again.')
        return

    output = CobaltEngine().download({
        'url': user_url_message,
    })

    # logging output
    print(f'handing request for {user_url_message} with output {output}\n'
          f'from {message.from_user.first_name}({message.from_user.id})')

    # handling output
    # handling stream, expect youtube because of slow download
    if output.get('status') == 'stream':

        # handling stream
        downloader = StreamDownloader(
            url=output.get('url'),
            output_path=str(Path.cwd() / 'downloads' / 'stream' / f'{message.from_user.id}' / f'{time.time():.0f}' / '[STREAM_FILENAME]')
        )

        # downloading stream
        try:
            downloaded_file = await downloader.download()
        except Exception as e:
            print(f'Error while downloading stream for {user_url_message}\n'
                  f'error message: {e}')
            return await message.reply_text('Error!, please try again later\n'
                                            f'message: `{e}`')

        # progress callback
        async def progress(current, total):
            print(
                f'uploading for {message.from_user.first_name}({message.from_user.id}) '
                f'{current * 100 / total:.1f}%'
                f'input: {user_url_message}'
            )

        # sending video
        await client.send_video(
            video=downloaded_file,
            chat_id=message.chat.id,
            supports_streaming=True,
            progress=progress)

        # delete downloaded file
        await downloader.delete()
        return

    if output.get('status') == 'redirect':
        await message.reply_text('Your request is being processed')
        await client.send_video(video=output.get("url"), chat_id=message.chat.id)
        return

    if output.get('status') == 'error':
        return message.reply_text('Error!, please try again later'
                                  f'message: `{output.get("text")}`')

    await message.reply_text('Error!, please try again later')