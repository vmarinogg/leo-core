/* Sample C file for Cartographer AST extraction tests. */

#include <stdlib.h>

typedef struct {
    char *path;
    int max_size;
} Config;

struct DataProcessor {
    Config *config;
    int count;
};

Config *load_config(const char *path) {
    Config *cfg = malloc(sizeof(Config));
    cfg->path = (char *)path;
    cfg->max_size = 1024;
    return cfg;
}

int process_record(struct DataProcessor *dp, const char *record) {
    dp->count++;
    return 0;
}
