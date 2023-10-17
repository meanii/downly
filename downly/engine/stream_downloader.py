import httpx
from downly.utils.fs_utils import make_sure_path_exists
from pathlib import Path


class StreamDownloader:
    def __init__(self, url, output_path, chunk_size=1024):
        self.url = url
        self.output_path = output_path
        self.chunk_size = chunk_size
        self.client = httpx.AsyncClient()

    async def download(self):
        """
        download file from url in stream mode
        :return:
        """

        SLOW_DOWNLOAD_THRESHOLD = 300  # 300 bytes
        SLOW_DOWNLOAD_TRIES = 30  # 30 times

        make_sure_path_exists(Path(self.output_path).parent)  # make sure parent directory exists
        print(f"downloading {self.url} to {self.output_path}")
        async with self.client.stream("GET", self.url) as response:

            # get file name and extension from stream response
            file_name = response.headers.get("Content-Disposition").split("filename=")[1].replace('"', "")
            self.output_path = self.output_path.replace("[STREAM_FILENAME]", f"{file_name}")

            with open(self.output_path, "wb") as file:
                async for chunk in response.aiter_bytes():
                    print(f"writing {len(chunk)} bytes to {self.output_path}")

                    # check if download slow less than 1kb/s
                    if len(chunk) < SLOW_DOWNLOAD_THRESHOLD:
                        SLOW_DOWNLOAD_TRIES -= 1
                        if SLOW_DOWNLOAD_TRIES == 0:
                            print(f"downloading slow less than {SLOW_DOWNLOAD_THRESHOLD} bytes/s, so closing operation.")
                            await self.delete()
                            raise Exception(f"download slow less than {SLOW_DOWNLOAD_THRESHOLD} bytes/s, deleted file")
                        print(f"downloading slow less than 1kb/s, {SLOW_DOWNLOAD_TRIES} tries left")

                    file.write(chunk)
                print(f"finished writing {self.output_path}")
        await self.client.aclose()  # close client
        return self.output_path

    async def delete(self):
        """
        delete file
        :return:
        """
        Path(self.output_path).unlink(missing_ok=True)
        print(f"deleted {self.output_path}")