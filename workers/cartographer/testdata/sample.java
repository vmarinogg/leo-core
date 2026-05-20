// Sample Java file for Cartographer AST extraction tests.

public class DataProcessor {
    private Config config;

    public DataProcessor(Config config) {
        this.config = config;
    }

    public Object process(Object record) {
        return record;
    }
}

interface Processable {
    Object process(Object record);
}

enum Status {
    ACTIVE,
    INACTIVE
}

class Config {
    private String path;

    public Config(String path) {
        this.path = path;
    }

    public String getPath() {
        return path;
    }
}
