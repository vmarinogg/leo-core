// Sample C# file for Cartographer AST extraction tests.

namespace Mom.Samples;

public class DataProcessor
{
    private Config _config;

    public DataProcessor(Config config)
    {
        _config = config;
    }

    public object Process(object record)
    {
        return record;
    }
}

public interface IProcessable
{
    object Process(object record);
}

public record Config(string Path, int MaxSize);

public enum Status
{
    Active,
    Inactive
}
