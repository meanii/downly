defmodule DownlyWorkerTest do
  use ExUnit.Case
  doctest DownlyWorker

  test "greets the world" do
    assert DownlyWorker.hello() == :world
  end
end
