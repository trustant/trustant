#!/bin/bash
source ./.env

echo "=== Ollama Models and Context Size ==="
curl -s "$OLLAMA_ENDPOINT/api/tags" | jq -r '.models[].name' | while read -r model; do
    ctx=$(curl -s "$OLLAMA_ENDPOINT/api/show" -d "{\"name\": \"$model\"}" | jq -r '
        .model_info // {} | to_entries[] | select(.key | test("context_length")) | .value // empty
    ')
    echo "$model -> context_length: ${ctx:-unknown}"
done
