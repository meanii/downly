defmodule DownlyWorker.Worker do
  @moduledoc """
  DownlyWorker is a Telegram bot that downloads files from the Internet.
  """
  use GenServer

  def start_link(_) do
    GenServer.start_link(__MODULE__, nil)
  end

  def init(_) do
    {:ok, nil}
  end

  def handle_call({:square_root, x}, _from, state) do
    IO.puts("process #{inspect(self())} calculating square root of #{x}")
    Process.sleep(1000)
    {:reply, :math.sqrt(x), state}
  end

  def handle_call(:get_queue, _from, state) do
    DownlyWorker.start()
    {:reply, :ok, state}
  end

  def handle_call({:download, message}, _from, state) do
    IO.puts("process #{inspect(self())} downloading #{message}")
    Process.sleep(1000)
    {:reply, :ok, state}
  end

  def handle_call({:upload, url}, _from, state) do
    IO.puts("process #{inspect(self())} uploading #{url}")
    Process.sleep(1000)
    {:reply, :ok, state}
  end

end
