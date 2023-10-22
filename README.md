# downly 🦉
> [downly](https://t.me/downlyrobot) is a simple telegram bot that can download files from the internet and upload them to telegram.

downly uses [wukko/cobalt](https://github.com/wukko/cobalt/) to download files. 
downly is written in [python](https://python.org) using [pyrogram](https://docs.pyrogram.org/) 

# self-hosting
I recommend using [docker compose](https://docker.com) to run downly, but you can also run it without docker.

- install docker compose  
```bash 
curl -o- https://get.docker.com | sh -x
```

- clone the repo
```bash
git clone https://github.com/meanii/downly
```
- create a `config.yaml` file and fill the required fields
- run `docker compose up -d`


## Supported services
we are highly relayed on [wukko/cobalt](https://github.com/wukko/cobalt/)'s api, so we can download from any service that is supported by cobalt.
check out the [cobalt's supported services](https://github.com/wukko/cobalt/tree/current#supported-services) for more info.

NOTE: we use [yt-dlp](https://github.com/yt-dlp/yt-dlp) to download YouTube videos for better speed and quality.

## credits
- [wukko](https://github.com/wukko) for [cobalt](https://github.com/wukko/cobalt/)
- [Dan](https://github.com/delivrance) for [pyrogram](https://github.com/pyrogram/pyrogram/)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)