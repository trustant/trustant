import importlib.machinery
import importlib.util
import io
import os
import select
import threading
import unittest
from pathlib import Path


def load_wrapper():
    path = Path(__file__).with_name("mcp-s3")
    loader = importlib.machinery.SourceFileLoader("trustable_mcp_s3", str(path))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


wrapper = load_wrapper()


class MCPWrapperTest(unittest.TestCase):
    def test_pump_forwards_short_message_without_waiting_for_eof(self):
        source_read, source_write = os.pipe()
        destination_read, destination_write = os.pipe()
        source = io.TextIOWrapper(io.BufferedReader(os.fdopen(source_read, "rb", buffering=0)))
        destination = io.TextIOWrapper(io.BufferedWriter(os.fdopen(destination_write, "wb", buffering=0)))
        thread = threading.Thread(target=wrapper.pump, args=(source, destination), daemon=True)
        thread.start()

        message = b'{"jsonrpc":"2.0","method":"initialize"}\n'
        os.write(source_write, message)
        readable, _, _ = select.select([destination_read], [], [], 1)

        self.assertEqual(readable, [destination_read])
        self.assertEqual(os.read(destination_read, len(message)), message)

        os.close(source_write)
        thread.join(timeout=1)
        source.close()
        destination.close()
        os.close(destination_read)

    def test_rewrite_filters_bucket_listing_and_normalizes_null(self):
        message = {
            "result": {
                "tools": [
                    {"name": "s3_list_buckets"},
                    {"name": "s3_list_objects"},
                ],
                "buckets": None,
            }
        }

        rewritten = wrapper.rewrite_message(message)

        self.assertEqual(rewritten["result"]["tools"], [{"name": "s3_list_objects"}])
        self.assertEqual(rewritten["result"]["buckets"], [])


if __name__ == "__main__":
    unittest.main()
