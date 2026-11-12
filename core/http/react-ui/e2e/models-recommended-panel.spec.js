import { test, expect } from "./coverage-fixtures.js";
import { mockLedger, stubRecommendations } from "./ledger-fixtures.js";
import { mockRecommended, recommendedAnswer } from "./recommended-fixtures.js";

// The "Best for this machine" shelf sits in the empty inspector of Models
// Explore. The server picks the models, the window each one installs with and
// whether it fits (GET /api/models/recommended); the shelf only draws that. With
// nothing installed it lists every model the server returned; once something is
// installed it narrows to the best fit and offers the rest behind a toggle.

const panel = (page) => page.getByTestId("recommended-models");
const toggle = (page) => page.getByTestId("recommended-models-toggle");
const grid = (page) => page.locator("#rec-models-content");

async function open(page, options = {}) {
  await mockLedger(page, options);
  await stubRecommendations(page);
  await page.goto("/app/models");
  await expect(panel(page)).toBeVisible({ timeout: 20_000 });
}

test.describe("Models gallery - recommended panel", () => {
  test("it is a section in the flow, not a dismissable card", async ({ page }) => {
    await open(page, { installed: [], loaded: [] });
    // No close button and no collapse: this is the one thing the page has to
    // say about the machine it runs on, not an interruption to be shut.
    await expect(panel(page).locator("button[aria-expanded]")).toHaveCount(0);
    await expect(panel(page).getByRole("button", { name: /dismiss|close/i })).toHaveCount(0);
    // And no card chrome, so it sits in the pane rather than on top of it.
    const border = await panel(page).evaluate((el) => getComputedStyle(el).borderTopWidth);
    expect(parseFloat(border)).toBe(0);
  });

  test("lists what the server returned, best fit first, with size, window and memory", async ({ page }) => {
    await open(page, { installed: [], loaded: [] });
    await expect(panel(page).getByRole("heading", { name: "Best for this machine" })).toBeVisible();
    const rows = grid(page).locator(".lane");
    await expect(rows).toHaveCount(4);
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    await expect(rows.nth(1).locator(".lane__tag")).toHaveText("Also fits");
    // One opinion per page: the others are alternatives, not runners-up worth
    // their own colour.
    await expect(grid(page).locator(".lane__tag--evidence")).toHaveCount(1);
    for (let i = 0; i < 4; i++) {
      await expect(rows.nth(i).getByRole("button", { name: "Install" })).toBeVisible();
      await expect(rows.nth(i)).toContainText(/\d+(\.\d+)? GB/);
      await expect(rows.nth(i)).toContainText("32K context");
      await expect(rows.nth(i)).toContainText(/in use/);
    }
  });

  test("a long model id wraps inside its row and never runs under the install button", async ({ page }) => {
    await open(page, { installed: [], loaded: [] });
    const row = grid(page).locator(".lane").first();
    const name = await row.locator(".lane__name").boundingBox();
    const button = await row.getByRole("button", { name: "Install" }).boundingBox();
    expect(name.x + name.width).toBeLessThanOrEqual(button.x);
    const pane = await page.getByTestId("discover-pane").boundingBox();
    expect(button.x + button.width).toBeLessThanOrEqual(pane.x + pane.width);
  });

  test("with a model installed it narrows to the best fit and the rest opens on request", async ({ page }) => {
    await open(page);
    const rows = grid(page).locator(".lane");
    await expect(rows).toHaveCount(1);
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    const more = toggle(page);
    await expect(more).toHaveAttribute("aria-expanded", "false");
    await expect(more).toHaveText("3 more that fit");
    await more.click();
    await expect(rows).toHaveCount(4);
    await expect(more).toHaveAttribute("aria-expanded", "true");
    await more.click();
    await expect(rows).toHaveCount(1);
  });

  test("renders nothing when the server has no model for this machine", async ({ page }) => {
    await mockLedger(page, { installed: [], loaded: [] });
    await mockRecommended(page, recommendedAnswer({ models: [] }));
    await page.goto("/app/models");
    await expect(page.getByTestId("discover-pane")).toContainText("Your host", { ignoreCase: true, timeout: 20_000 });
    await expect(panel(page)).toHaveCount(0);
  });

  test("renders nothing when the server cannot answer", async ({ page }) => {
    await mockLedger(page, { installed: [], loaded: [] });
    await page.route("**/api/models/recommended", (route) => route.fulfill({ status: 500, json: { error: "boom" } }));
    await page.goto("/app/models");
    await expect(page.getByTestId("discover-pane")).toContainText("Your host", { ignoreCase: true, timeout: 20_000 });
    await expect(panel(page)).toHaveCount(0);
  });

  test("install names the entry, pins it and carries the window the server fitted", async ({ page }) => {
    await mockLedger(page, { installed: [], loaded: [] });
    await stubRecommendations(page);
    const installs = [];
    await page.route("**/api/models/install/*", (route) => {
      const url = new URL(route.request().url());
      installs.push({ id: decodeURIComponent(url.pathname.split("/").pop()), query: Object.fromEntries(url.searchParams) });
      return route.fulfill({ json: { jobID: "job-1" } });
    });
    await page.goto("/app/models");
    await expect(panel(page)).toBeVisible({ timeout: 20_000 });
    const first = grid(page).locator(".lane").first();
    const id = (await first.locator(".lane__name").getAttribute("title")).trim();
    await first.getByRole("button", { name: "Install" }).click();
    await expect.poll(() => installs.length).toBe(1);
    expect(installs[0].id).toBe(id);
    expect(installs[0].query).toEqual({ variant: id, context_size: "32768" });
  });
});
