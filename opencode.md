# Trustable App Development Guide

This is a serverless application with a TypeScript/React frontend and a Python backend.

## Important Rules

- Never try to build or deploy; this is managed automatically when you edit sources
- Use tools directly to inspect and edit files. Do not answer with a plan when a
  small requested code change can be done immediately.
- For code-change requests, after you locate the target file, call `edit` or
  `write` before your final answer. Do not describe an edit unless
  it has already been applied.
- After applying an edit, give a short final answer and stop. Do not keep
  re-reading files unless validation is necessary.
- If `edit` fails because `oldString` has multiple matches, do not repeat the
  same edit. If the requested change should apply to every matching occurrence,
  retry the edit once with `replaceAll: true`. If only one occurrence should
  change, re-read the file and use a larger unique `oldString`, or use `write`
  with the complete updated file content.
- Never expose internal reasoning, channel markers, handoff summaries, or
  implementation notes as assistant text.
- Tool arguments must be plain JSON values. For paths and globs, do not wrap the
  string value in extra quote characters and do not include channel markers.
- If a tool call fails because a path or pattern is too narrow, retry with a
  simpler broader search such as `delete`, `Delete`, `red`, `bg-red`,
  `text-red`, `destructive`, or the visible button label.
- To initialize always use `ops ide init` that will execute ALL the private actions in `init` package.
- Never create a backend server; create new public action providing an endpoint.
- Never create or edit `__main__.py` files, use the following tools:
    - `action-new` to create an action
    - `action-add-secret` to add a new env var / secret
    - `action-add-s3` to add S3 service
    - `action-add-postgresql` to add SQL database service
    - `action-add-milvus` to add vector database service
    - `action-add-redis` to add redis cache service

## Architecture

- **Frontend**: TypeScript with React and Tailwind CSS, sources under `src/`
- **Backend**: Python serverless actions, sources are under `packages/<package>/<action>/`
- If you need a public endpoint, use `v1` as package, choose an alphanumeric name (can contain '-'), create a public action with `action-new` and you get an endpoint `/api/my/<package>/<action>`

## API Endpoints

- Backend APIs are public actions available at `/api/my/<package>/<action>` (package is usually `v1`)
- To access an action with streaming output, given `<proto>://<user>.<domain>`, POST to `<proto>://stream.<domain>/web/<package>/<action>` (returns a stream of JSON objects)
- To invoke a private action, for initialization for example, use `action-invoke`

## Initializations

To initialize database schemas, cache objects, add files to s3 buckets and more, create private actions in package `init` using `action-new` with `public: false`.

Private actions are the same as public ones but with `#--web false` and are not exposed as HTTP endpoints. Invoke them with `action-invoke`.

Make the init actions always:
- incremental
- idempotent
- not destructive
for example:
- create a database with `CREATE TABLE IF NOT EXISTS`
- add fields with `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`

## Dependencies

### Frontend
-  add frontend dependencies to `package.json` then run `npm install`

### Backend

- When you need a SQL database, **ALWAYS** use the tool `action-add-postgresql` and the provided context and the already provided `psycopg` library to access it. Never try to add requirements or use directly database connections.

- When you need redis, **ALWAYS** use the tool `action-add-redis` and the provided context and the already provided `redis` library. Never try to add requirements or use directly redis connections.

- When you need s3, **ALWAYS** use the tool `action-add-s3` and the provided context and the already provided `boto3` library. Never try to add requirements or use directly s3 connections.

- When you need a vector database, **ALWAYS** use the tool `action-add-milvus` and the provided context and the already provided `pymilvus` library. Never try to add requirements or use directly milvus connections.

- Always use the `action-requirements` tool to add Python libraries. Never edit `requirements.txt` directly.

## Creating Backend Actions

- **ALWAYS** use the `action-new` tool to create new API endpoints. Never create `__main__.py` or action directories directly.

- Never edit `__main__.py`. Edit `packages/<package>/<action>/<module>.py` instead (where `<module>` is `<action>` with `-` replaced by `_`). The main function of this module is invoked with the request parameters and a context object to access services.
