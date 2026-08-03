import { test, expect } from "./coverage-fixtures.js";

// The "Recommended for your hardware" strip defaults its own prominence off the
// installed-model count and remembers both the collapse choice and a dismissal,
// so every assertion here is about state that must survive a reload.

const DISMISS_KEY = "localai-models-recommended-dismissed";
const COLLAPSE_KEY = "localai-models-recommended-collapsed";

const REC_MODELS = [
  { name: "tiny-chat", description: "Tiny", backend: "llama-cpp", installed: false, tags: ["chat"] },
  { name: "small-chat", description: "Small", backend: "llama-cpp", installed: false, tags: ["chat"] },
];

function listResponse(installedModels) {
  return {
    models: REC_MODELS,
    allBackends: ["llama-cpp"],
    allTags: ["chat"],
    availableModels: REC_MODELS.length,
    installedModels,
    totalPages: 1,
    currentPage: 1,
  };
}

const ESTIMATES = {
  "tiny-chat": { sizeBytes: 512 * 1024 * 1024, sizeDisplay: "512.0 MB", estimates: { 4096: { vramBytes: 700 * 1024 * 1024, vramDisplay: "700.0 MB" } } },
  "small-chat": { sizeBytes: 1024 * 1024 * 1024, sizeDisplay: "1.00 GB", estimates: { 4096: { vramBytes: 1400 * 1024 * 1024, vramDisplay: "1.40 GB" } } },
};

// installedModels drives the panel's default state, so each test picks its own.
async function mockGallery(page, installedModels) {
  // Registered first so the more specific routes below take precedence:
  // Playwright matches the most recently added handler.
  await page.route("**/api/models*", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify(listResponse(installedModels)) }),
  );
  await page.route("**/api/models/estimate/*", (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(ESTIMATES[name] || {}) });
  });
  // CPU-only host, which is the branch that shows the "no GPU detected" note.
  await page.route("**/api/resources", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ type: "cpu", available: false, gpus: [] }) }),
  );
}

const panel = (page) => page.getByTestId("recommended-models");
const toggle = (page) => page.getByTestId("recommended-models-toggle");
const grid = (page) => page.locator("#rec-models-content");

async function gotoModels(page) {
  await page.goto("/app/models");
  await expect(panel(page)).toBeVisible({ timeout: 20_000 });
}

test.describe("Models gallery - recommended panel prominence", () => {
  test("it is a section in the flow, not a dismissable card", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);
    await expect(panel(page)).toBeVisible();
    // No close button and no collapse: this is the one thing the page has to
    // say about the machine it runs on, not an interruption to be shut.
    await expect(panel(page).locator("button[aria-expanded]")).toHaveCount(0);
    await expect(panel(page).getByRole("button", { name: /dismiss|close/i })).toHaveCount(0);
    // And no card chrome, so it sits in the pane rather than on top of it.
    const border = await panel(page).evaluate((el) => getComputedStyle(el).borderTopWidth);
    expect(parseFloat(border)).toBe(0);
  });






  test("recommendations render and their install buttons still work", async ({ page }) => {
    await mockGallery(page, 0);
    let installed = null;
    await page.route("**/api/models/install/*", (route) => {
      installed = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
      return route.fulfill({ contentType: "application/json", body: JSON.stringify({ uuid: "job-1" }) });
    });
    await gotoModels(page);

    // Ranked candidates read in fit order, so these are lanes now rather than
    // a grid of equal cards.
    const row = grid(page).locator(".lane", { hasText: "tiny-chat" });
    await expect(row).toBeVisible();
    await expect(row.getByText("512.0 MB")).toBeVisible();
    await row.getByRole("button", { name: "Install" }).click();

    await expect.poll(() => installed).toBe("tiny-chat");
  });

  test("the best fit is called out, the rest are alternatives", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);
    const rows = grid(page).locator(".lane");
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    // One opinion per page: the others are alternatives, not runners-up worth
    // their own colour.
    await expect(grid(page).locator(".lane__tag--evidence")).toHaveCount(1);
  });
});
