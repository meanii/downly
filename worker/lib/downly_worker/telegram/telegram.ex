defmodule DownlyWorker.Telegram do
  @moduledoc """
  Telegram API module.
  This module is responsible for interacting with the Telegram API.
  """

  @doc """
  Get the bot's information.
  This function will return the bot's information from the Telegram API.
  """
  @spec me() :: {:ok, map()} | {:error, any()}
  def me() do
    {:ok, result} = Redix.command(:redix, ["GET", "downly:me"])

    case result do
      nil ->
        {:ok, result} =
          Telegram.Api.request(Application.get_env(:downly_worker, :telegram_token), "getMe")

        Redix.command(:redix, ["SET", "telegram:me", Poison.encode!(result)])
        {:ok, result}

      _ ->
        {:ok, Poison.decode!(result)}
    end
  end
end
