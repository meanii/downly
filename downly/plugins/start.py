from pyrogram import Client, filters


@Client.on_message(filters=filters.command('start'))
async def start(client, message):
    # content
    start_message = (
        f'hellow 🦉,   {client.get_me().first_name} here!\n\n'
        'I am a simple bot that can help you to save what you love.\n'
    )
    await message.reply_text(start_message)
