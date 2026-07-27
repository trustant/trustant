import importlib.machinery
import importlib.util
import unittest
from pathlib import Path


def load_wrapper():
    path = Path(__file__).with_name("redis-mcp")
    loader = importlib.machinery.SourceFileLoader("trustable_redis_mcp", str(path))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


wrapper = load_wrapper()


class RedisMCPWrapperTest(unittest.TestCase):
    def test_scopes_exact_keys_and_foreign_prefixes(self):
        self.assertEqual(
            wrapper.scope_arguments("get", {"key": "session:1"}, "app:"),
            {"key": "app:session:1"},
        )
        self.assertEqual(
            wrapper.scope_arguments("get", {"key": "other:session:1"}, "app:"),
            {"key": "app:other:session:1"},
        )
        self.assertEqual(
            wrapper.scope_arguments("get", {"key": "app:session:1"}, "app:"),
            {"key": "app:session:1"},
        )

    def test_hash_field_is_not_mistaken_for_the_hash_key(self):
        self.assertEqual(
            wrapper.scope_arguments(
                "hget",
                {"name": "users", "key": "email"},
                "app:",
            ),
            {"name": "app:users", "key": "email"},
        )

    def test_scopes_scan_and_vector_index_defaults(self):
        self.assertEqual(
            wrapper.scope_arguments("scan_all_keys", {}, "app:"),
            {"pattern": "app:*"},
        )
        self.assertEqual(
            wrapper.scope_arguments("create_vector_index_hash", {}, "app:"),
            {"index_name": "app:vector_index", "prefix": "app:doc:"},
        )

    def test_filters_global_and_unknown_tools(self):
        response = {
            "result": {
                "tools": [
                    {"name": "get", "description": "Read a key."},
                    {"name": "dbsize"},
                    {"name": "future_unreviewed_tool"},
                ]
            }
        }
        rewritten = wrapper.rewrite_response(response)
        self.assertEqual([tool["name"] for tool in rewritten["result"]["tools"]], ["get"])
        self.assertIn("namespace server-side", rewritten["result"]["tools"][0]["description"])

        with self.assertRaises(wrapper.PolicyError):
            wrapper.scope_arguments("dbsize", {}, "app:")
        with self.assertRaises(wrapper.PolicyError):
            wrapper.scope_arguments("future_unreviewed_tool", {}, "app:")

    def test_denied_tool_call_returns_protocol_level_failure(self):
        forwarded, denied = wrapper.prepare_request(
            {
                "jsonrpc": "2.0",
                "id": 7,
                "method": "tools/call",
                "params": {"name": "info", "arguments": {}},
            },
            "app:",
        )
        self.assertIsNone(forwarded)
        self.assertTrue(denied["result"]["isError"])
        self.assertNotIn("app:", denied["result"]["content"][0]["text"])


if __name__ == "__main__":
    unittest.main()
