# Trustable App Development Guide

This is a serverless application with a TypeScript/React frontend and a Python backend.

## Important Rules

- Never try to build or deploy; this is managed automatically when you edit sources.
- Never run foreground dev servers or watchers such as `npm run dev`, `vite`, or `ops ide devel`; Trustable already manages the dev server. To verify frontend changes, use bounded checks such as `curl http://localhost:5173`, or `timeout <seconds> ...`.
- All initialization must go in private actions in the `setup` package. Executing `ops ide setup` invokes ALL of them. The setup actions must be idempotent. They are invoked automatically when deploying, but NOT when developing — so whenever you change one of them you must be explicit and run `ops ide setup`.
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

To initialize database schemas, cache objects, add files to s3 buckets and more, create private actions in package `setup` using `action-new` with `public: false`.

Private actions are the same as public ones but with `#--web false` and are not exposed as HTTP endpoints. Invoke them with `action-invoke`.

All setup actions are invoked together by `ops ide setup`. They are run automatically when deploying, but NOT when developing — so whenever you change a setup action you must be explicit and run `ops ide setup` for the change to take effect.

Use a dedicated setup action per kind of resource, and always run `ops ide setup` after creating or changing one:

- **Tables** — when you need a new table, create the action `setup/database` (or update it if it already exists), then run `ops ide setup`. Never create tables or seed data in the database outside of `setup/database`. Make every statement idempotent (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`).
- **Redis keys** — when you need to prepare redis with certain keys, create the action `setup/cache` (or update it if it already exists), then run `ops ide setup`. Never initialize redis keys outside of `setup/cache`. Make it idempotent (only set keys that are missing, e.g. `SET ... NX`).
- **Milvus collections** — when you need a new collection, create the action `setup/collection` (or update it if it already exists), then run `ops ide setup`. Never create collections outside of `setup/collection`. Make it idempotent (check the collection exists before creating it).
- **S3 data** — when you need to preload files into the `<user>-data` bucket, create the action `setup/upload` (or update it if it already exists), then run `ops ide setup`. Never seed bucket objects outside of `setup/upload`. Make it idempotent (only upload objects that are missing or changed). The `<user>-web` bucket is initialized separately from the content in the `public` folder — do not upload web content via `setup/upload`.

Make the setup actions always:
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

## Databases, Redis, Bucket restrictions

Retrieve your `<user>` with `ops util whoami`. These restrictions are enforced by the platform — operations outside them fail.

- **Postgres**: the database is named after `<user>`; the schema is `<user>_schema` and is the default. Do not create or use other databases or schemas.
- **Milvus**: the database is named after `<user>`. Do not create or use other databases.
- **Redis**: keys must be prefixed with `<user>:` — keys without this prefix are not writable.
- **S3**: there are exactly two writable buckets:
  - `<user>-data` — private. Use this for application data; preload it with the `setup/upload` action (see Initializations).
  - `<user>-web` — public. Never store private data here. Its content comes from the `public` folder, uploaded automatically on deploy — do not write to it directly.

  The S3 MCP cannot list buckets, so assume only `<user>-data` and `<user>-web` exist.
