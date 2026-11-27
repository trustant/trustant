# Headroom experiment (closed)

The Headroom experiment was closed on 2026-07-11 and the component was removed
from the Trustant product, image, configuration UI, launch path, and E2E
runner.

## Result

The controlled provider matrix did not produce a successful Headroom run:

| Provider | Model | Mode | Duration | Result |
| --- | --- | --- | ---: | --- |
| BestIA | `qwen3.6:35b` | direct | 556 s | failed completion gate |
| BestIA | `qwen3.6:35b` | Headroom | 379 s | failed completion gate |
| Regolo | `qwen3.6-27b` | direct | 605 s | runner exposed delegated-session accounting gap |
| Regolo | `qwen3.6-27b` | Headroom | >720 s | stalled and interrupted |

Measured token savings were 0.56% on BestIA and 1.60% on the interrupted
Regolo run. They did not compensate for the added runtime complexity or produce
a completed workflow. Ollama Cloud was not evaluated because no dedicated test
credential was available.

## Decision

Headroom is not a supported or experimental Trustant component. OpenCode uses
the configured provider directly. Issue 98 reliability continues through
deterministic contracts, compaction recovery, completion gates, browser checks,
and E2E coverage. The delegated-session accounting fix discovered during this
experiment remains part of the provider runner because it is independent of
Headroom.
