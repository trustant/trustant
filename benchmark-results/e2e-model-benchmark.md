# Trustable E2E Model Benchmark

Report locale per confrontare provider, modello e comportamento operativo sui prompt E2E.

| Data UTC | Scenario | Provider | Modello | Small model | App | Durata | Esito runner | Checker | Evidenze | Note |
| --- | --- | --- | --- | --- | --- | ---: | --- | --- | --- | --- |
| 2026-07-07 08:31 | Homepage semplice | Regolo / api.nuvolaris.io | qwen3-coder-next | gpt-oss-20b | regqcoder1 | 52.4s | PASS | PASS | `src/pages/Index.tsx` modificato con sezione "Prova Regolo" e contatore 42 | MCP configurati; prompt completato correttamente. |
| 2026-07-07 08:42 | Stack Nuvolaris | Regolo / api.nuvolaris.io | qwen3-coder-next | gpt-oss-20b | regstackqc | 386s | FAIL | PASS | sessione `ses_0c4417959ffejvl92y2RMEEOCX`; MCP connessi: milvus, openserverless, postgres, redis, s3 | Ha letto struttura, `src/App.tsx`, `package.json`, poi ha tentato di leggere `.env`; OpenCode lo ha lasciato in stato `read .env` running/ask. Nessuna implementazione Stack completata. |
| 2026-07-07 08:56 | Stack Nuvolaris | Regolo / api.nuvolaris.io | gpt-oss-120b | gpt-oss-20b | regstkgpt120 | 29s | FAIL | PASS | sessione `ses_0c434d59bffeJ83DPi9XNTOpx7`; OpenCode log: `AI_InvalidResponseDataError: Expected 'id' to be a string.` | Failure diversa: tool-call non valida (`list` con input `{}`), abortita prima di qualunque implementazione. |
| 2026-07-07 08:59 | Stack Nuvolaris | Regolo / api.nuvolaris.io | qwen3.6-27b | gpt-oss-20b | regstkq3627 | 213s | PASS | PASS | sessione `ses_0c431b486ffeRU9WxigIEG32rR`; build OK; Vite root e `/#/stack` 200; MCP action tools usati: `action-new`, `action-add-postgresql`, `action-add-redis`, `action-add-s3` | Ha creato `/stack`, action `v1/stack-check`, deploy, verifica live. Risultati dichiarati: PostgreSQL/Redis/S3 `ok` con `stack-ok-42`, MongoDB `non configurato`. |
| 2026-07-07 09:25 | Stack Nuvolaris | Regolo / api.nuvolaris.io | qwen3.5-122b | gpt-oss-20b | regstkq35122 | 28s | FAIL | PASS | log: `benchmark-results/logs/e2e-stack-regolo-qwen3.5-122b-20260707-092548.log`; sessione `ses_0c419e6efffeMcI8JV31t901Lk`; MCP connessi dopo rilancio | Ha letto `opencode.json`, `.openserverless-contract.md`, `src`, `packages`; seconda risposta vuota, poi `opencode.<domain>` 502. Nessuna evidenza utile sul comportamento Mongo. Config riportata a `qwen3.6-27b`. |
| 2026-07-07 09:33 | Stack Nuvolaris | Regolo / api.nuvolaris.io | minimax-m2.5 | gpt-oss-20b | regstkmini25 | 25s | FAIL | PASS | log: `benchmark-results/logs/e2e-stack-regolo-minimax-m2.5-20260707-093345.log`; sessione `ses_0c412a33fffeY1EnVVSv8DqPWA`; OpenCode log: `Invalid model name passed in model=minimax-m2.5` | Non confrontabile sul merito: `/api/status` elenca il modello, ma `/chat/completions` lo rifiuta per questa chiave/catalogo. Config riportata a `qwen3.6-27b`. |
| 2026-07-07 09:46 | Stack Nuvolaris | Regolo / api.nuvolaris.io | mistral-small-4-119b | gpt-oss-20b | regstkmistral | 223s | FAIL | PASS | log: `benchmark-results/logs/e2e-stack-regolo-mistral-small-4-119b-20260707-094606.log`; action `packages/v1/stack-check`; UI edit parziale in `src/pages/Index.tsx`; checker PASS | Ha prodotto codice, ma con tool errors: `edit` senza `newString` e `oldString` non trovato. Su MongoDB ha tentato un mapping improprio via Milvus e poi ha segnato `non configurato (Milvus client issues)`. Config riportata a `qwen3.6-27b`. |
| 2026-07-07 09:57 | Stack Nuvolaris | BestIA locale / Ollama | qwen3.6:35b | bestia/coding:30b | beststack36 | manual stop | FAIL | PASS | log: `benchmark-results/logs/e2e-stack-bestia-qwen3.6-35b-20260707-095751.log`; sessione `ses_0c3fc9506ffeANyZ1Ih3CqeQR8`; tool `question` running su MongoDB | Ha letto struttura e MCP, poi ha chiesto all'utente come gestire MongoDB mancante. Non inventa come Mistral, ma per prodotto e runner e' failure: il prompt user-level non deve richiedere scelta tecnica. Config riportata a Regolo `qwen3.6-27b`. |
| 2026-07-07 10:54 | Stack Nuvolaris post MCP Mongo | Regolo / api.nuvolaris.io | qwen3.6-27b | gpt-oss-20b | trumng01 | 10.4m | PASS | PASS | E2E Playwright issue98 completo: `2 passed`; sessione `ses_0c3d1a3a7ffeRE2VIM69Rak5PI`; MCP connessi: mongodb, milvus, openserverless, postgres, redis, s3 | Dopo l'implementazione MCP Mongo/guardrail, il runner completa senza `question` tecniche. Ha usato gli action tool ufficiali (`action-new`, `action-add-redis`, `action-add-postgresql`, `action-add-s3`) e il checker finale e' passato. |

## Prompt Stack

```text
Vorrei una sezione o pagina "Stack Nuvolaris" nella home dell'app.

Deve verificare in modo visibile lettura e scrittura dei componenti dello stack: Redis, MongoDB, Postgres e S3.

Per ogni componente usa le risorse gia' disponibili nell'ambiente Trustable/OpenServerless. Scrivi un valore preciso con chiave o record "stack-e2e-{nonce}" e valore "stack-ok-42", poi rileggi lo stesso valore e mostra in UI una tabella con: componente, stato, valore letto, ultimo aggiornamento.

Non simulare un risultato positivo: se un componente non e' configurato o non e' raggiungibile, mostralo chiaramente come "non configurato" o "errore". Alla fine verifica che l'app parta, esegui il checker OpenServerless e lascia nel README una breve nota su come riprovare la verifica Stack.
```

## Osservazioni

- Il prompt Stack resta user-level: l'utente non deve sapere cosa sono `.env`, MCP o secret.
- `qwen3.6-27b` e' il primo modello Regolo che completa davvero lo scenario Stack.
- `qwen3.6-27b` mostra comunque un gap funzionale: tratta MongoDB come non configurato, mentre il requisito utente chiedeva di verificarlo insieme agli altri componenti.
- `qwen3-coder-next` si blocca su accesso `.env`; serve un guardrail/permission flow che impedisca lo stallo senza rendere tecnico il prompt utente.
- `gpt-oss-120b` e `qwen3.5-122b` non sono stati confrontabili sul merito applicativo: falliscono prima, rispettivamente per tool-call non valida e per sessione OpenCode caduta/502.
- `minimax-m2.5` non e' stato confrontabile: catalogo/status e completion API non sono allineati per la chiave usata nel test.
- `mistral-small-4-119b` e' produttivo ma non affidabile sul flusso agentico: genera codice e action, ma sbaglia uso dell'edit tool e introduce un mapping MongoDB -> Milvus non richiesto.
- BestIA locale `qwen3.6:35b` riconosce correttamente il gap MongoDB, ma usa il tool `question` per chiedere all'utente cosa fare; il runner oggi non intercetta questo caso via pending-question API e puo' restare appeso.
- Il checker issue98 passa in tutti i run, quindi non stiamo vedendo drift su `ops action` o modifiche vietate rilevate dal checker.
- Dopo il fix MCP Mongo, una nuova app pulita espone `mcp.mongodb` quando il post-login config lo rende disponibile e il prompt Stack passa senza domande tecniche.

## Sintesi provvisoria

| Modello | Valutazione Stack | Nota principale |
| --- | --- | --- |
| qwen3.6-27b | Migliore candidato attuale | Completa end-to-end, usa gli MCP action; dopo il fix MCP Mongo passa anche con `mcp.mongodb` disponibile. |
| BestIA qwen3.6:35b | Buona diagnosi, non autonomo | Capisce MongoDB mancante e chiede cosa fare; da correggere con guardrail/runner per evitare domande tecniche. |
| mistral-small-4-119b | Parziale / instabile | Produce codice ma fallisce su edit tool e confonde MongoDB con Milvus. |
| qwen3-coder-next | Bloccato | Si incastra su `.env`, senza completare la feature. |
| qwen3.5-122b | Non valutabile | Sessione/OpenCode cade dopo letture iniziali. |
| gpt-oss-120b | Non valutabile | Tool-call non valida lato OpenCode/proxy. |
| minimax-m2.5 | Non valutabile | Rimosso/non disponibile su Regolo completion API. |

Conclusione: per questo scenario Regolo `qwen3.6-27b` e' il baseline operativo. Dopo il fix MCP Mongo, il prompt Stack passa anche con `mcp.mongodb` esposto dal config ufficiale. BestIA locale `qwen3.6:35b` resta promettente sulla diagnosi, ma va guidato a prendere decisioni di fallback autonome quando un servizio richiesto non e' disponibile. Il prossimo miglioramento non e' solo scegliere modello: serve rafforzare il runner/guardrail contro tool errors, stalli su secret e tool `question` tecnici.
