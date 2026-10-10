"""Playwright UX check for minute2win-server's page (STD-008 style): drive the real UI, assert the arm -> boot -> report flow,
assert no horizontal page scroll at phone and desktop widths, and save screenshots as evidence.
   python3 test_ui.py <base-url> <screenshot-dir>"""
import json, sys, urllib.request
from playwright.sync_api import sync_playwright, expect

BASE, SHOTS = sys.argv[1].rstrip("/"), sys.argv[2]
MAC = "52:54:00:12:34:56"


def post(path, body):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"}, method="POST")
    return urllib.request.urlopen(req).status


def no_hscroll(page, label):
    over = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert over <= 0, f"{label}: page scrolls horizontally by {over}px"


with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page(viewport={"width": 1280, "height": 900})
    page.goto(BASE + "/")
    expect(page.locator("select[name=manifest] option")).to_have_count(1)  # win11-pro; manifests/examples/ is not deployable
    expect(page.locator("#armed-empty")).to_be_visible()
    expect(page.locator("#armed")).to_be_hidden()
    expect(page.locator("input[name=token]")).to_be_hidden()  # this server has no token

    # a bad MAC is refused with the server's own explanation
    page.fill("input[name=mac]", "not-a-mac")
    page.click("button[type=submit]")
    expect(page.locator("#msg")).to_contain_text("Not armed: MAC must look like")

    # arm
    page.fill("input[name=mac]", MAC.upper().replace(":", "-"))
    page.select_option("select[name=manifest]", "win11-pro")
    page.click("button[type=submit]")
    expect(page.locator("#msg")).to_contain_text(f"Armed {MAC} for win11-pro")
    expect(page.locator("#armed tbody tr")).to_have_count(1)
    expect(page.locator("#armed tbody")).to_contain_text(MAC)
    page.screenshot(path=f"{SHOTS}/armed-desktop.png", full_page=True)

    # the target network-boots: the arm is consumed and a deployment appears
    script = urllib.request.urlopen(f"{BASE}/boot/{MAC}").read().decode()
    assert "wimboot" in script, script
    key = json.loads(urllib.request.urlopen(f"{BASE}/manifest/{MAC}").read())["report"]["key"]  # what WinPE receives
    expect(page.locator("#armed tbody tr")).to_have_count(0)
    expect(page.locator(".card")).to_contain_text("booting: boot")

    # the engine reports phases; finished under a minute shows as "under"
    assert post("/api/report", {"key": key, "macs": [MAC.upper()], "phase": "downloaded", "status": "running", "model": "QEMU Standard PC", "serial": "VM-0001",
                                "timing": {"ready": 3.1, "partitioned": 4.0, "downloaded": 31.5}}) == 204
    expect(page.locator(".card .phase")).to_have_text("running: downloaded")
    expect(page.locator(".card .laps")).to_contain_text("downloaded 31.5s")
    assert post("/api/report", {"key": key, "macs": [MAC], "phase": "done", "status": "done", "model": "QEMU Standard PC", "serial": "VM-0001",
                                "timing": {"ready": 3.1, "partitioned": 4.0, "downloaded": 31.5, "applied": 52.2, "done": 58.1}}) == 204
    expect(page.locator(".card .clock")).to_have_text("58.1 s")
    expect(page.locator(".card .clock")).to_have_class("clock under")
    expect(page.locator(".card .laps")).to_contain_text("ready 3.1s · partitioned 4s · downloaded 31.5s · applied 52.2s · done 58.1s")  # time order
    no_hscroll(page, "desktop")
    page.screenshot(path=f"{SHOTS}/deployed-desktop.png", full_page=True)

    # disarm path
    page.fill("input[name=mac]", "52:54:00:00:00:02")
    page.click("button[type=submit]")
    expect(page.locator("#armed tbody tr")).to_have_count(1)
    page.get_by_role("button", name="Disarm").click()
    expect(page.locator("#armed tbody tr")).to_have_count(0)
    expect(page.locator("#msg")).to_contain_text("Disarmed 52:54:00:00:00:02")
    # disarming a machine that already booted must not read as success
    page.fill("input[name=mac]", "52:54:00:00:00:03")
    page.click("button[type=submit]")
    expect(page.locator("#armed tbody tr")).to_have_count(1)
    urllib.request.urlopen(f"{BASE}/boot/52:54:00:00:00:03").read()
    page.get_by_role("button", name="Disarm").click()
    expect(page.locator("#msg")).to_contain_text("was not armed")
    expect(page.locator("#msg")).to_have_class("err")

    for scheme in ("light", "dark"):
        ctx = browser.new_context(viewport={"width": 375, "height": 800}, color_scheme=scheme)
        m = ctx.new_page()
        m.goto(BASE + "/")
        expect(m.locator(".card")).to_have_count(2)
        no_hscroll(m, f"phone {scheme}")
        m.screenshot(path=f"{SHOTS}/phone-{scheme}.png", full_page=True)
        ctx.close()
    browser.close()
print("ui: all checks passed")
