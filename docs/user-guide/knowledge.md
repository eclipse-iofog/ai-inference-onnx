# Add a knowledge item

Put a file named `knowledge` at the top of the model directory, beside `config.pbtxt`. One name per line. The line rules are in the [repository](../reference/repository.md) page.

```
wafer-labels
```

The model stays not ready until `{KNOWLEDGE_PATH}/wafer-labels/` exists. The default knowledge root is `/knowledge`. The engine does not read the files inside the item.
