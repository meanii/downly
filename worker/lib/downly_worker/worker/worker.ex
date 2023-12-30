defmodule DownlyWorker.Worker do
  @moduledoc """
  DownlyWorker is a Telegram bot that downloads files from the Internet.
  """
  use GenServer
  require Logger

  def start_link(_) do
    GenServer.start_link(__MODULE__, nil)
  end

  def init(_) do
    {:ok, nil}
  end

  def handle_call({:process, message}, _from, state) do
    Logger.info("processing QueueID: #{message["id"]} URL: #{message["url"]}")
    Process.sleep(1000)
    {:reply, :ok, state}
  end

end
