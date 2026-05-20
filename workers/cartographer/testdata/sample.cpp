// Sample C++ file for Cartographer AST extraction tests.

#include <string>

struct Config {
    std::string path;
    int max_size;
};

typedef struct {
    std::string name;
} Record;

class DataProcessor {
public:
    explicit DataProcessor(const Config& config) : config_(config) {}

    std::string process(const std::string& record) {
        return record;
    }

private:
    Config config_;
};

Config load_config(const std::string& path) {
    Config cfg;
    cfg.path = path;
    cfg.max_size = 1024;
    return cfg;
}
