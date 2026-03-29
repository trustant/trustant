Rule: an endpoint maps in an action in folder `packages/<package>/<name>` , functions is a synonym for action.

Rule: assume the packages referenced by the tools are already available and preinstalled.

Rule: always insert the snippet of code at the beginning of the requested sections, unless they are already there (do not add it twice).

Implement the following tools in folder tools/ using this template:

```
import { tool } from "@opencode-ai/plugin"

export default tool({
  description: "<description>",
  args: {
    <arg>: tool.schema.<type>().describe("<arg description>"),
  },
  async execute(args) {
    <tool logic>
    return `<results>`
  },
})
```

# tool: bash (replaces the default)

this tool execute bash commands  using `/bin/bash -c` using Bun api

# tool new-api-endpoint

This tool create a new api endpoint

## parameters
Receive an <endpoint> as parameter
- if there are no  '/' in the <endpoint> use   `v1` as <package> and  the <endpoint> as <name>,
- if there is one `/` use the part before the `/` as <package> and <name> the part after
-  if there are more than one `/` return error
-  ensure <package> and <name> only contains letters, numbers and '-' and starts with letter
- <module> is the <name> with '-' replaced with '_' (example: 'new-user' becomes 'new_user')

## generation

- create a folder `packages/<package>/<name>`

- write a file `packages/<package>/<name>/__main__.py` with the content:

```
#--kind python:default
#--web true
# Note: this timeout is 5 minutes - 10 minutes is max allowed
#--timeout 300000
## begin define-params
## end define-params
import types, os, <module>
def main(args):
  context = types.SimpleNamespace()
  ## begin read-params
  ## end read-params
  try:
    return { "body": <module>.main(args, ctx=context) }
  except Exception as e:
    import traceback
    traceback.print_exc()
    return {
      "body": {"error": str(e) },
      "statusCode": 500
    }
```

- write a file `packages/<package>/<name>/<module>.py` with the content:

```
def main(args, ctx=None):
    inp = args.get("input", "<module>")
    out = inp
    return out
```

## returns

the resulting api url /api/my/<package>/<name>

# tool add-secret

## parameters
Receive an <endpoint> as parameter

## generation

This tool add a new secret <MY_SECRET> to an endpoint/action/function

Requires the endpoint and the secret

Fistchecks the secret is available in `.env`, if not warn the user and suggest to add it to the environment


- adds in `__main__.py` section "define-params"
```
#--param <MY_SECRET> "$<MY_SECRET>"
```
- adds in `__main__.py` section "read-params"

```
context.<MY_SECRET> = args.get("<MY_SECRET>", os.getenv("<MY_SECRET>"))
```

## returns

informations on the updated context


# tool add-s3

## parameters
Receive an <endpoint> as parameter

## generation

This tool add s3 to the context of an endpoint/action/function when you need to use it, making available as
- context.S3_CLIENT the s3 client
- context.S3_DATA the s3 data bucket, private
- context.S3_WEB the s3 web bucket, public
- context.S3_PUBLIC the public url to use S3

- adds in `__main__.py` section "define-params"

```
#--param S3_HOST "$S3_HOST"
#--param S3_PORT "$S3_PORT"
#--param S3_ACCESS_KEY "$S3_ACCESS_KEY"
#--param S3_SECRET_KEY "$S3_SECRET_KEY"
#--param S3_BUCKET_DATA "$S3_BUCKET_DATA"
#--param S3_BUCKET_STATIC "$S3_BUCKET_STATIC"
#--param S3_PUBLIC "$OPSDEV_S3"
```

- adds in `__main__.py` section "read-params"

```
import boto3
from botocore.client import Config
host = args.get("S3_HOST", os.getenv("S3_HOST"))
port = args.get("S3_PORT", os.getenv("S3_PORT"))
url = f"http://{host}:{port}"
key = args.get("S3_ACCESS_KEY", os.getenv("S3_ACCESS_KEY"))
sec = args.get("S3_SECRET_KEY", os.getenv("S3_SECRET_KEY"))
cfg = Config(signature_version='s3v4')
context.S3_CLIENT = boto3.client('s3', region_name='us-east-1', endpoint_url=url, aws_access_key_id=key, aws_secret_access_key=sec )
context.S3_DATA = args.get("S3_BUCKET_DATA", os.getenv("S3_BUCKET_DATA"))
context.S3_WEB = args.get("S3_BUCKET_STATIC", os.getenv("S3_BUCKET_STATIC"))
context.S3_PUBLIC = args.get("S3_PUBLIC", os.getenv("OPSDEV_S3"))
```

## returns

informations on the updated context

# tool add-redis

## parameters
Receive an <endpoint> as parameter

## generation

This tool adds a context.REDIS connection  and a context.REDIS_PREFIX to an endpoint/action/function

- adds in `__main__.py` section "define-params"
```
#--param REDIS_URL "$REDIS_URL"
#--param REDIS_PREFIX "$REDIS_PREFIX"
```

- adds in `__main__.py` section "read-params"

```
import redis
context.REDIS = redis.from_url(args.get("REDIS_URL", os.getenv("REDIS_URL")))
context.REDIS_PREFIX = args.get("REDIS_PREFIX", os.getenv("REDIS_PREFIX"))
```
## returns

informations on the updated context

# tool add-postgresql

## parameters
Receive an <endpoint> as parameter

## generation

This tool adds a context.POSTGRESQL connection  to an endpoint/action/function:

- adds in `__main__.py` section "define-params"

```
#--param POSTGRES_URL "$POSTGRES_URL"
```

- adds in `__main__.py` section "read-params"

```
import psycopg
dburl = args.get("POSTGRES_URL", os.getenv("POSTGRES_URL"))
context.POSTGRESQL = psycopg.connect(dburl)
```
## returns

informations on the updated context

# tool add-milvus

## parameters
Receive an <endpoint> as parameter

## generation

This tool adds a context.MILVUS connection   to an endpoint/action/function:

- adds in `__main__.py` section "define-params"

```
#--param MILVUS_HOST "$MILVUS_HOST"
#--param MILVUS_PORT "$MILVUS_PORT"
#--param MILVUS_DB_NAME "$MILVUS_DB_NAME"
#--param MILVUS_TOKEN "$MILVUS_TOKEN"
```

- adds in `__main__.py` section "read-params"

```
from pymilvus import MilvusClient
uri = f"http://{args.get('MILVUS_HOST', os.getenv('MILVUS_HOST'))}"
token = args.get("MILVUS_TOKEN", os.getenv("MILVUS_TOKEN"))
db_name = args.get("MILVUS_DB_NAME", os.getenv("MILVUS_DB_NAME"))
context.MILVUS = MilvusClient(uri=uri, token=token, db_name=db_name)
```
## returns

informations on the updated context



