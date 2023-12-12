defmodule DownlyWorker do
  @moduledoc """
  DownlyWorker is a Telegram bot that downloads files from the Internet.
  """
  @timeout 60_000_000
  require Logger


  def start() do
    {:ok, result} = Redix.command(:redix, ["RPOP", "downly:queue"])
    case result do
      nil ->
        Logger.info("NOTHING TO DO.")
        :timer.sleep(1000)
        start()
      _ ->
        message = Poison.encode!(result)
        Logger.info(message)
        download(message)
        :timer.sleep(1000)
        start()
    end
  end


  defp download(message) do
    Task.async(fn ->
      :poolboy.transaction(
        :worker,
        fn pid ->
          # Let's wrap the genserver call in a try - catch block. This allows us to trap any exceptions
          # that might be thrown and return the worker back to poolboy in a clean manner. It also allows
          # the programmer to retrieve the error and potentially fix it.
          try do
            GenServer.call(pid, {:download, message})
          catch
            e, r -> Logger.error("poolboy transaction caught error: #{inspect(e)}, #{inspect(r)}")
            :ok
          end
        end,
        @timeout
      )
    end)
  end

end
