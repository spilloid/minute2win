"""Real MCP SDK and optional browser regressions; no real target boots.

pip install mcp==1.30.0 [playwright==1.55.0]
python tests/mcp/test_client.py <server-binary> <artifact-dir> [--ui]
M2W_UI_BROWSER optionally selects an installed Chromium browser.
Hosts bind loopback, use synthetic MACs/temporary state and never start PXE.
"""
import asyncio
import contextlib
import json
import os
from pathlib import Path
import runpy
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

import httpx
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamable_http_client

BINARY = Path(sys.argv[1]).resolve()
ARTIFACTS = Path(sys.argv[2]).resolve()
ARTIFACTS.mkdir(parents=True, exist_ok=True)
PROFILE = {"schemaVersion": 1, "image": {"source": "https", "uri": "https://images.lab.internal/w11/install.wim", "sha256": "a" * 64, "edition": "Windows 11 Pro"}}
PASSWORD = "synthetic-browser-test-passphrase"


@contextlib.contextmanager
def host(with_auth):
    with tempfile.TemporaryDirectory(prefix="m2w-mcp-") as temp:
        root = Path(temp)
        profiles = root / "profiles"
        profiles.mkdir()
        (profiles / "lab-win11.json").write_text(json.dumps(PROFILE), encoding="utf-8")
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env = {k: v for k, v in os.environ.items() if not k.startswith("M2W_")}
        token = secrets.token_hex(32) if with_auth else ""
        env["M2W_TOKEN"] = token
        args = [str(BINARY), f"-listen=127.0.0.1:{port}", f"-manifests={profiles}", f"-data={root / 'data'}", "-image-base=https://boot.lab.internal/windows11", "-open=false"]
        if with_auth:
            args.append("-mcp")
        with (ARTIFACTS / ("authenticated-host.log" if with_auth else "preview-host.log")).open("w", encoding="utf-8") as log:
            process = subprocess.Popen(args, env=env, stdout=log, stderr=log, creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        raise AssertionError("test host exited early; inspect artifact log")
                    try:
                        with urllib.request.urlopen(base + "/healthz", timeout=0.3) as response:
                            if response.status == 200:
                                break
                    except (OSError, TimeoutError):
                        time.sleep(0.05)
                else:
                    raise AssertionError("test host did not become ready")
                yield base, token
            finally:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


async def sdk_check(base, token):
    async with httpx.AsyncClient(headers={"Authorization": f"Bearer {token}"}) as http:
        async with streamable_http_client(base + "/mcp", http_client=http) as (read, write, _):
            async with ClientSession(read, write) as session:
                assert (await session.initialize()).serverInfo.name == "minute2win"
                assert len((await session.list_tools()).tools) >= 28
                for resource in (await session.list_resources()).resources:
                    await session.read_resource(resource.uri)
                assert (await session.list_prompts()).prompts[0].name == "guided_deployment"
                await session.get_prompt("guided_deployment")
                args = {"mac": "52:54:00:00:00:71", "manifest": "lab-win11", "request_id": "sdk-validation-71"}
                first = await session.call_tool("machine_arm", args)
                assert not first.isError
                run_id = first.structuredContent["run_id"]
                again = await session.call_tool("machine_arm", args)
                assert again.structuredContent["replayed"] is True and again.structuredContent["run_id"] == run_id
                pending = await session.call_tool("deployment_get", {"mac": args["mac"], "run_id": run_id})
                assert "armed" in pending.structuredContent and pending.structuredContent["terminal"] is False
                assert not (await session.call_tool("machine_disarm", {"mac": args["mac"]})).isError
                assert (await session.call_tool("machine_disarm", {"mac": args["mac"]})).isError
                for username, role in [("fixture-operator", "operator"), ("fixture-viewer", "viewer")]:
                    result = await session.call_tool("account_put", {"username": username, "role": role, "profiles": ["lab-win11"], "password": PASSWORD})
                    assert not result.isError
                result = await session.call_tool("account_token_create", {"username": "fixture-viewer"})
                viewer_token = result.structuredContent["token"]
                async with httpx.AsyncClient(headers={"Authorization": f"Bearer {viewer_token}"}) as viewer_http:
                    async with streamable_http_client(base + "/mcp", http_client=viewer_http) as (vr, vw, _):
                        async with ClientSession(vr, vw) as viewer:
                            await viewer.initialize()
                            denied = await viewer.call_tool("machine_arm", args)
                            assert denied.isError and denied.structuredContent["error"]["code"] == "forbidden"
    params = StdioServerParameters(command=str(BINARY), args=["-mcp-stdio", "-mcp-url", base + "/mcp"], env={"M2W_TOKEN": token})
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            identity = await session.call_tool("whoami", {})
            assert identity.structuredContent["identity"]["role"] == "admin"
            assert len((await session.list_tools()).tools) >= 28
    print("Official MCP SDK: HTTP, stdio, discovery, request replay, monitor/disarm and RBAC passed.")


def browser_check(base):
    from playwright.sync_api import sync_playwright, expect
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=os.environ.get("M2W_UI_BROWSER") or None)
        for width, scheme in [(1280, "light"), (390, "dark")]:
            context = browser.new_context(viewport={"width": width, "height": 900}, color_scheme=scheme)
            page = context.new_page()
            page.goto(base + "/")
            expect(page.locator('input[name="password"]')).to_be_visible()
            assert page.evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth")
            page.screenshot(path=str(ARTIFACTS / f"login-{width}.png"), full_page=True)
            page.fill('input[name="username"]', "fixture-operator")
            page.fill('input[name="password"]', PASSWORD)
            page.get_by_role("button", name="Sign in", exact=True).click()
            expect(page.locator("#arm")).to_be_visible()
            expect(page.locator('input[name="token"]')).to_have_count(0)
            for last in (81, 82):
                mac = f"52:54:00:00:{width % 100:02}:{last}"
                page.fill('input[name="mac"]', mac)
                page.get_by_role("button", name="Arm for wipe").click()
                expect(page.locator("#msg")).to_contain_text("Armed " + mac)
            expect(page.locator("#armed tbody tr")).to_have_count(2)
            assert page.evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth")
            page.screenshot(path=str(ARTIFACTS / f"signed-in-{width}.png"), full_page=True)
            for remaining in (1, 0):
                page.get_by_role("button", name="Disarm", exact=True).first.click()
                expect(page.locator("#armed tbody tr")).to_have_count(remaining)
            page.get_by_role("button", name="Sign out", exact=True).click()
            expect(page.locator('input[name="password"]')).to_be_visible()
            page.fill('input[name="username"]', "fixture-viewer")
            page.fill('input[name="password"]', PASSWORD)
            page.get_by_role("button", name="Sign in", exact=True).click()
            expect(page.locator("#arm")).to_have_count(0)
            expect(page.get_by_text("Your account can monitor deployments.", exact=False)).to_be_visible()
            page.screenshot(path=str(ARTIFACTS / f"viewer-{width}.png"), full_page=True)
            context.close()
        browser.close()
    print("Browser: one login, repeated arms, logout, viewer role and desktop/phone overflow passed.")


with host(True) as (base, token):
    asyncio.run(sdk_check(base, token))
    if "--ui" in sys.argv:
        browser_check(base)

if "--ui" in sys.argv:
    with host(False) as (base, _):
        script = Path(__file__).resolve().parents[1] / "ui" / "test_ui.py"
        saved = sys.argv
        os.environ["M2W_UI_PROFILE"] = "lab-win11"
        try:
            sys.argv = [str(script), base, str(ARTIFACTS)]
            runpy.run_path(str(script), run_name="__main__")
        finally:
            sys.argv = saved
