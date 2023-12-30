defmodule DownlyWorker.Utils.Files do
  @moduledoc """
  Files utility module.
  This module is responsible for handling files.
  """

  @doc """
  Create a cache directory.
  This function will create a cache directory.
  """
  @spec create_cache_dir(String.t()) :: {:ok, String.t()} | :error
  def create_cache_dir(path) do
    cache_path = Path.join(File.cwd!(), "/cache" <> path)
    case File.mkdir_p(cache_path) do
      {:ok, _} -> {:ok, cache_path}
      {:error, _} -> :error
    end
  end

  @doc """
  Save a file.
  This function will save a file.
  """
  @spec save_file(String.t(), String.t()) :: {:ok, String.t()} | :error
  def save_file(path, content) do
    dirname = Path.dirname(path)
    cache_path = create_cache_dir(dirname)
    filepath = Path.join(cache_path, Path.basename(path))
    case File.write(filepath, content) do
      :ok -> {:ok, path}
      :error -> :error
    end
  end


end
