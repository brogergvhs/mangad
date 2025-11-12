import sys
from seleniumbase import Driver

if len(sys.argv) < 2:
    print("Usage: python selenium_fetch.py <url>")
    sys.exit(1)

url = sys.argv[1]
driver = Driver(uc=True, headless=True)

try:
    driver.uc_open_with_reconnect(url, 4)
    driver.uc_gui_click_captcha()
    html = driver.page_source
    print(html)
    driver.quit()
    sys.exit(0)
except Exception as e:
    driver.quit()
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)

