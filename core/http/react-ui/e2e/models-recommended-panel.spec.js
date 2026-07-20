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
  test("first visit with nothing installed shows the panel expanded", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);

    await expect(toggle(page)).toHaveAttribute("aria-expanded", "true");
    await expect(grid(page)).toBeVisible();
    await expect(grid(page).getByText("tiny-chat")).toBeVisible();
  });

  test("a user with models installed gets it collapsed by default", async ({ page }) => {
    await mockGallery(page, 12);
    await gotoModels(page);

    await expect(toggle(page)).toHaveAttribute("aria-expanded", "false");
    await expect(grid(page)).toBeHidden();
    // Collapsed is a summary, not a removal: the heading stays on the page.
    await expect(panel(page).getByText("Recommended for your hardware")).toBeVisible();
    await expect(panel(page).getByText("2 models suggested")).toBeVisible();
  });

  test("the collapsed summary expands again on activation", async ({ page }) => {
    await mockGallery(page, 12);
    await gotoModels(page);

    await expect(grid(page)).toBeHidden();
    await toggle(page).click();

    await expect(toggle(page)).toHaveAttribute("aria-expanded", "true");
    await expect(grid(page)).toBeVisible();
    await expect(page.evaluate((k) => localStorage.getItem(k), COLLAPSE_KEY)).resolves.toBe("0");
  });

  test("the collapse choice persists across a reload", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);
    await expect(grid(page)).toBeVisible();

    await toggle(page).click();
    await expect(grid(page)).toBeHidden();

    await page.reload();
    await expect(panel(page)).toBeVisible({ timeout: 20_000 });
    await expect(toggle(page)).toHaveAttribute("aria-expanded", "false");
    await expect(grid(page)).toBeHidden();
  });

  test("dismissing it persists across a reload", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);

    await panel(page).getByRole("button", { name: "Dismiss recommendations" }).click();
    await expect(panel(page)).toHaveCount(0);
    await expect(page.evaluate((k) => localStorage.getItem(k), DISMISS_KEY)).resolves.toBe("1");

    await page.reload();
    // The table is the marker that the page finished rendering without the panel.
    await expect(page.locator("table tbody tr").first()).toBeVisible({ timeout: 20_000 });
    await expect(panel(page)).toHaveCount(0);
  });

  test("the toggle is keyboard operable and exposes its state", async ({ page }) => {
    await mockGallery(page, 12);
    await gotoModels(page);

    await toggle(page).focus();
    await expect(toggle(page)).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(toggle(page)).toHaveAttribute("aria-expanded", "true");
    // aria-controls must resolve to the region it actually shows and hides.
    await expect(toggle(page)).toHaveAttribute("aria-controls", "rec-models-content");
    await expect(grid(page)).toBeVisible();
  });

  test("recommendations render and their install buttons still work", async ({ page }) => {
    await mockGallery(page, 0);
    let installed = null;
    await page.route("**/api/models/install/*", (route) => {
      installed = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
      return route.fulfill({ contentType: "application/json", body: JSON.stringify({ uuid: "job-1" }) });
    });
    await gotoModels(page);

    const card = grid(page).locator(".rec-models-item", { hasText: "tiny-chat" });
    await expect(card).toBeVisible();
    await expect(card.getByText("512.0 MB")).toBeVisible();
    await card.getByRole("button", { name: "Install" }).click();

    await expect.poll(() => installed).toBe("tiny-chat");
  });
});
