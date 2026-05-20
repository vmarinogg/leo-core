# Sample Ruby file for Cartographer AST extraction tests.

module Utilities
  def self.format(val)
    val.to_s
  end
end

class DataProcessor
  def initialize(config)
    @config = config
  end

  def process(record)
    record
  end
end

class Config
  attr_reader :path

  def initialize(path)
    @path = path
  end
end

def load_config(path)
  Config.new(path)
end
