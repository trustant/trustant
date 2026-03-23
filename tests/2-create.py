#!/usr/bin/env -S uv run --with playwright
# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright"]
# ///

import atexit
import signal
from playwright.sync_api import sync_playwright, expect

BASE_URL = "http://localhost:8910"

APP_NAME = "trureact"
APP_PASSWORD = "trureact"
APP_REPO = "trustable-ai/tru-react"

_browser = None

def wait_and_close():
    if _browser:
        input("\nPress Enter to close the browser...")
        _browser.close()

atexit.register(wait_and_close)
signal.signal(signal.SIGINT, lambda *_: exit(1))


def main():
    global _browser
    with sync_playwright() as p:
        _browser = p.chromium.launch(headless=False)
        browser = _browser
        page = browser.new_page()

        # 1. Connect to localhost:8910 and follow redirect
        print("Connecting to localhost:8910...")
        page.goto(BASE_URL)
        page.wait_for_load_state("networkidle")

        # 2. If configuration modal appears, wait for it and click Continue
        config_done = page.locator("#configDone")
        if config_done.count() > 0:
            print("Configuration in progress, waiting for completion...")
            config_done.wait_for(state="visible", timeout=300000)
            page.get_by_role("button", name="Continue").click()
            page.wait_for_load_state("networkidle")
            print("Configuration done.")

        # 3. Check there is the "Add App" button
        print("Checking for Add App button...")
        add_app_btn = page.get_by_role("button", name="Add App")
        expect(add_app_btn).to_be_visible()
        print("Add App button found.")

        # 3. Click "Add App" to open the modal
        print("Opening Add Application modal...")
        add_app_btn.click()

        # 4. Fill in the application form
        print(f"Filling in: name={APP_NAME}, repo={APP_REPO}")
        page.fill("#appName", APP_NAME)
        page.fill("#appPassword", APP_PASSWORD)
        page.fill("#appRepo", APP_REPO)

        # 5. Click "Create"
        print("Clicking Create...")
        page.get_by_role("button", name="Create").click()

        # 6. Wait for the "Application Created" result
        print("Waiting for application creation...")
        page.wait_for_selector("text=Application Created", timeout=120000)
        print("Application created successfully!")

        # 7. Click OK to dismiss
        page.get_by_role("button", name="OK").click()
        page.wait_for_load_state("networkidle")

        # 8. Verify the app appears in the list
        print("Verifying app appears in list...")
        expect(page.get_by_role("heading", name=APP_NAME)).to_be_visible()
        print(f"App '{APP_NAME}' is visible in the list.")

        print("Test passed!")
        _browser = None
        input("Press Enter to close the browser...")
        browser.close()


if __name__ == "__main__":
    main()
