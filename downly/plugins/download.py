from pyrogram import filters, Client
from pyrogram.types import Message
from downly.downly import Downly
from downly.engine.cobalt import CobaltEngine
from downly.utils.validator import validate_url


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
    if output.get('status') == 'stream':
        # TODO: handle stream
        # we can use ffmpeg to stream
        await message.reply_text('currently not supported!')
        return

    if output.get('status') == 'redirect':
        await message.reply_text('Your request is being processed')
        await client.send_video(video=output.get("url"), chat_id=message.chat.id)
        return

    await message.reply_text('Error!, please try again later')