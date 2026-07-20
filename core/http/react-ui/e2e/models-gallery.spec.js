import { test, expect } from "./coverage-fixtures.js";

const MOCK_MODELS_RESPONSE = {
  models: [
    {
      name: "llama-model",
      description: "A llama model",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
      // The listing carries only the declaration flag. Describing variants
      // costs the server a network probe each, so the description lives
      // behind /api/models/variants/:id and is fetched on demand.
      has_variants: true,
    },
    {
      name: "whisper-model",
      description: "A whisper model",
      backend: "whisper",
      installed: true,
      tags: ["transcript"],
    },
    {
      name: "stablediffusion-model",
      description: "An image model",
      backend: "stablediffusion",
      installed: false,
      tags: ["sd"],
    },
    {
      name: "unknown-model",
      description: "No backend",
      backend: "",
      installed: false,
      tags: [],
    },
  ],
  allBackends: ["llama-cpp", "stablediffusion", "whisper"],
  allTags: ["chat", "sd", "transcript"],
  availableModels: 4,
  installedModels: 1,
  totalPages: 1,
  currentPage: 1,
};

const MOCK_GPU_RESOURCES_RESPONSE = {
  type: "gpu",
  available: true,
  gpus: [
    {
      index: 0,
      name: "Mock GPU",
      vendor: "nvidia",
      total_vram: 12 * 1024 * 1024 * 1024,
      used_vram: 2 * 1024 * 1024 * 1024,
      free_vram: 10 * 1024 * 1024 * 1024,
      usage_percent: 16.7,
    },
  ],
  aggregate: {
    total_memory: 12 * 1024 * 1024 * 1024,
    used_memory: 2 * 1024 * 1024 * 1024,
    free_memory: 10 * 1024 * 1024 * 1024,
    usage_percent: 16.7,
    gpu_count: 1,
  },
};

const MOCK_ESTIMATES = {
  "llama-model": {
    sizeBytes: 4 * 1024 * 1024 * 1024,
    sizeDisplay: "4.00 GB",
    estimates: {
      8192: {
        vramBytes: 8 * 1024 * 1024 * 1024,
        vramDisplay: "8.00 GB",
      },
    },
  },
  "whisper-model": {
    sizeBytes: 1 * 1024 * 1024 * 1024,
    sizeDisplay: "1.00 GB",
    estimates: {
      8192: {
        vramBytes: 2 * 1024 * 1024 * 1024,
        vramDisplay: "2.00 GB",
      },
    },
  },
  "stablediffusion-model": {
    sizeBytes: 8 * 1024 * 1024 * 1024,
    sizeDisplay: "8.00 GB",
    estimates: {
      8192: {
        vramBytes: 16 * 1024 * 1024 * 1024,
        vramDisplay: "16.00 GB",
      },
    },
  },
};

test.describe("Models Gallery - Backend Features", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    // Wait for the table to render
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("backend column header is visible", async ({ page }) => {
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible();
  });

  test("backend badges shown in table rows", async ({ page }) => {
    const table = page.locator("table");
    await expect(
      table.locator(".badge", { hasText: "llama-cpp" }),
    ).toBeVisible();
    await expect(
      table.locator(".badge", { hasText: /^whisper$/ }),
    ).toBeVisible();
  });

  test("backend dropdown is visible", async ({ page }) => {
    await expect(
      page.locator("button", { hasText: "All Backends" }),
    ).toBeVisible();
  });

  test("clicking backend dropdown opens searchable panel", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    await expect(
      page.locator('input[placeholder="Search backends..."]'),
    ).toBeVisible();
  });

  test("typing in search filters dropdown options", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    const searchInput = page.locator('input[placeholder="Search backends..."]');
    await searchInput.fill("llama");

    // llama-cpp option should be visible, whisper should not
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await expect(dropdown.locator("text=llama-cpp")).toBeVisible();
    await expect(dropdown.locator("text=whisper")).not.toBeVisible();
  });

  test("selecting a backend updates the dropdown label", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    // Click the llama-cpp option within the dropdown (not the table badge)
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // The dropdown button should now show the selected backend instead of "All Backends"
    await expect(
      page.locator("button span", { hasText: "llama-cpp" }),
    ).toBeVisible();
  });

  test("expanded row shows backend in detail", async ({ page }) => {
    // Click the first model row to expand it
    await page.locator("tr", { hasText: "llama-model" }).click();

    // The detail view should show Backend label and value
    const detail = page.locator('td[colspan="8"]');
    await expect(detail.locator("text=Backend")).toBeVisible();
    // The Backend DetailRow renders before the Variants section, which lists a
    // per-variant backend badge of its own, so scope to the first match.
    await expect(detail.locator("text=llama-cpp").first()).toBeVisible();
  });
});

const BACKEND_USECASES_MOCK = {
  "llama-cpp": ["chat", "embeddings", "vision", "token_classify"],
  whisper: ["transcript"],
  stablediffusion: ["image"],
};

const EMPTY_FILTERED_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: [],
  availableModels: 0,
  totalPages: 1,
  currentPage: 1,
};

test.describe("Models Gallery - Multi-select Filters", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/backends/usecases", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(BACKEND_USECASES_MOCK),
      });
    });
    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("multi-select toggle: click Chat, TTS, then Chat again", async ({
    page,
  }) => {
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });

    await chatBtn.click();
    await expect(chatBtn).toHaveClass(/active/);

    await ttsBtn.click();
    await expect(chatBtn).toHaveClass(/active/);
    await expect(ttsBtn).toHaveClass(/active/);

    // Click Chat again to deselect it
    await chatBtn.click();
    await expect(chatBtn).not.toHaveClass(/active/);
    await expect(ttsBtn).toHaveClass(/active/);
  });

  test('"All" clears selection', async ({ page }) => {
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const allBtn = page.locator(".filter-btn", { hasText: "All" });

    await chatBtn.click();
    await expect(chatBtn).toHaveClass(/active/);

    await allBtn.click();
    await expect(allBtn).toHaveClass(/active/);
    await expect(chatBtn).not.toHaveClass(/active/);
  });

  test("query param sent correctly with multiple filters", async ({ page }) => {
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });

    // Click Chat and wait for its request to settle
    await chatBtn.click();
    await page.waitForResponse((resp) => resp.url().includes("/api/models"));

    // Now click TTS and capture the resulting request
    const [request] = await Promise.all([
      page.waitForRequest((req) => {
        if (!req.url().includes("/api/models")) return false;
        const u = new URL(req.url());
        const tag = u.searchParams.get("tag");
        return tag && tag.split(",").length >= 2;
      }),
      ttsBtn.click(),
    ]);

    const url = new URL(request.url());
    const tags = url.searchParams.get("tag").split(",").sort();
    expect(tags).toEqual(["chat", "tts"]);
  });

  test("backend greys out unavailable filters", async ({ page }) => {
    // Select llama-cpp backend via dropdown
    await page.locator("button", { hasText: "All Backends" }).click();
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // Wait for filter state to update
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });
    const sttBtn = page.locator(".filter-btn", { hasText: "STT" });
    const imageBtn = page.locator(".filter-btn", { hasText: "Image" });

    // TTS, STT, Image should be disabled for llama-cpp
    await expect(ttsBtn).toBeDisabled();
    await expect(sttBtn).toBeDisabled();
    await expect(imageBtn).toBeDisabled();

    // Chat, Embeddings, Vision, NER should remain enabled
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const embBtn = page.locator(".filter-btn", { hasText: "Embeddings" });
    const visBtn = page.locator(".filter-btn", { hasText: "Vision" });
    const nerBtn = page.locator(".filter-btn", { hasText: "NER" });
    await expect(chatBtn).toBeEnabled();
    await expect(embBtn).toBeEnabled();
    await expect(visBtn).toBeEnabled();
    await expect(nerBtn).toBeEnabled();
  });

  test("backend clears incompatible filters", async ({ page }) => {
    // Select TTS filter first
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });
    await ttsBtn.click();
    await expect(ttsBtn).toHaveClass(/active/);

    // Now select llama-cpp backend (which doesn't support TTS)
    await page.locator("button", { hasText: "All Backends" }).click();
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // TTS should be auto-removed from selection
    await expect(ttsBtn).not.toHaveClass(/active/);
  });
});

test.describe("Models Gallery - Fits In GPU Filter", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });

    await page.route("**/api/resources", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_GPU_RESOURCES_RESPONSE),
      });
    });

    await page.route("**/api/models/estimate/*", (route) => {
      const url = new URL(route.request().url());
      const id = decodeURIComponent(url.pathname.split("/").pop() || "");
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_ESTIMATES[id] || {}),
      });
    });

    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("fits toggle is visible when GPU resources are available", async ({
    page,
  }) => {
    await expect(page.getByText("Fits in GPU")).toBeVisible();
  });

  test("enabling fits filter hides models that exceed available VRAM", async ({
    page,
  }) => {
    await expect(
      page.locator("tr", { hasText: "stablediffusion-model" }),
    ).toBeVisible();

    // The shared <Toggle> visually hides its native input (opacity:0;w:0;h:0),
    // so .check() can't interact with it directly — click the visible track.
    await page
      .locator("label.filter-bar-group__toggle", { hasText: "Fits in GPU" })
      .locator(".toggle__track")
      .click();

    await expect(
      page.locator("tr", { hasText: "stablediffusion-model" }),
    ).toHaveCount(0);
    await expect(page.locator("tr", { hasText: "llama-model" })).toBeVisible();
    // Unknown estimate stays visible until an explicit non-fit verdict exists.
    await expect(
      page.locator("tr", { hasText: "unknown-model" }),
    ).toBeVisible();
  });

  test("fits filter state persists after reload", async ({ page }) => {
    await page
      .locator("label.filter-bar-group__toggle", { hasText: "Fits in GPU" })
      .locator(".toggle__track")
      .click();
    await page.reload();
    await expect(page.getByLabel("Fits in GPU")).toBeChecked();
  });
});

test.describe("Models Gallery - Empty State", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      const url = new URL(route.request().url());
      const tag = url.searchParams.get("tag");
      const body =
        tag === "chat" ? EMPTY_FILTERED_RESPONSE : MOCK_MODELS_RESPONSE;

      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("shows empty state for filtered-out results and clear filters restores the gallery", async ({
    page,
  }) => {
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const allBtn = page.locator(".filter-btn", { hasText: "All" });

    await chatBtn.click();

    await expect(page.locator(".empty-state-title")).toHaveText(
      "No models found",
    );
    await expect(page.locator(".empty-state-text")).toHaveText(
      "No models match your current search or filters.",
    );

    const clearBtn = page.getByRole("button", { name: "Clear filters" });
    await expect(clearBtn).toBeVisible();
    await expect(page.locator("tr", { hasText: "llama-model" })).toHaveCount(0);

    await clearBtn.click();

    await expect(allBtn).toHaveClass(/active/);
    await expect(chatBtn).not.toHaveClass(/active/);
    await expect(page.locator(".empty-state")).toHaveCount(0);
    await expect(page.locator("tr", { hasText: "llama-model" })).toBeVisible();
  });
});

// The variant description the companion endpoint returns for llama-model.
// memory_bytes is omitempty server-side, so the mlx variant deliberately
// carries no key at all: the UI must render that as unknown, never 0 B.
const MOCK_VARIANTS_RESPONSE = {
  variants: [
    {
      model: "llama-model",
      backend: "llama-cpp",
      memory_bytes: 4 * 1024 * 1024 * 1024,
      fits: true,
      is_base: true,
    },
    {
      model: "llama-model-q8",
      backend: "llama-cpp",
      memory_bytes: 8 * 1024 * 1024 * 1024,
      fits: true,
      is_base: false,
    },
    {
      model: "llama-model-mlx",
      backend: "mlx",
      fits: true,
      is_base: false,
    },
    {
      model: "llama-model-f16",
      backend: "llama-cpp",
      memory_bytes: 40 * 1024 * 1024 * 1024,
      fits: false,
      is_base: false,
    },
  ],
  auto_selected: "llama-model-q8",
};

test.describe("Models Gallery - Variant picker", () => {
  // installUrls records every install request so a test can assert both the
  // presence and the absence of the ?variant= parameter.
  let installUrls;
  // variantUrls records every companion-endpoint request. It is what proves
  // the description is fetched lazily and cached, rather than being paid for
  // by every row on page load.
  let variantUrls;
  // Held requests let a test observe the in-flight state rather than racing it.
  let releaseVariants;

  test.beforeEach(async ({ page }) => {
    installUrls = [];
    variantUrls = [];
    releaseVariants = null;
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/models/install/**", (route) => {
      installUrls.push(route.request().url());
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ jobID: "variant-install" }),
      });
    });
    await page.route("**/api/models/variants/**", async (route) => {
      variantUrls.push(route.request().url());
      if (releaseVariants) await releaseVariants;
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_VARIANTS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  const variantRow = (page) => page.locator("tr", { hasText: "llama-model" }).first();
  const plainRow = (page) =>
    page.locator("tr", { hasText: "stablediffusion-model" }).first();
  const openMenu = (page) =>
    variantRow(page).getByRole("button", { name: "Choose a variant" }).click();

  test("the listing alone fetches no variant descriptions", async ({ page }) => {
    // The whole point of the companion endpoint: a page load costs zero
    // probes no matter how many entries declare variants.
    await expect(page.locator("tbody tr").first()).toBeVisible();
    expect(variantUrls).toHaveLength(0);
  });

  test("an entry that declares variants shows the split-button chevron", async ({
    page,
  }) => {
    await expect(
      variantRow(page).getByRole("button", { name: "Choose a variant" }),
    ).toBeVisible();
  });

  test("an entry without variants renders no chevron", async ({ page }) => {
    await expect(
      plainRow(page).getByRole("button", { name: "Choose a variant" }),
    ).toHaveCount(0);
    // and still offers an ordinary install
    await expect(
      plainRow(page).locator("button.btn-primary"),
    ).toHaveCount(1);
  });

  test("an entry without variants fetches nothing even when expanded", async ({
    page,
  }) => {
    await plainRow(page).click();
    await expect(page.locator('td[colspan="8"]')).toBeVisible();
    expect(variantUrls).toHaveLength(0);
  });

  test("plain Install sends no variant parameter", async ({ page }) => {
    await plainRow(page).locator("button.btn-primary").click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).not.toContain("variant=");
  });

  test("opening the menu fetches the description once and caches it", async ({
    page,
  }) => {
    await openMenu(page);
    await expect(page.locator(".action-menu")).toBeVisible();
    await expect.poll(() => variantUrls.length).toBe(1);
    expect(variantUrls[0]).toContain("/api/models/variants/llama-model");

    // Close and reopen: the cached answer must be reused.
    await page.keyboard.press("Escape");
    await openMenu(page);
    await expect(
      page.locator(".action-menu__item", { hasText: "llama-model-q8" }),
    ).toBeVisible();
    expect(variantUrls).toHaveLength(1);
  });

  test("the menu shows a loading state while the description is in flight", async ({
    page,
  }) => {
    let unblock;
    releaseVariants = new Promise((resolve) => {
      unblock = resolve;
    });
    await openMenu(page);
    await expect(page.locator(".action-menu")).toContainText("Loading variants");
    unblock();
    await expect(
      page.locator(".action-menu__item", { hasText: "llama-model-q8" }),
    ).toBeVisible();
    await expect(page.locator(".action-menu")).not.toContainText(
      "Loading variants",
    );
  });

  test("the auto-selected variant is marked in the menu", async ({ page }) => {
    await openMenu(page);
    const menu = page.locator(".action-menu");
    await expect(menu).toBeVisible();
    const autoItem = menu.locator(".action-menu__item", {
      hasText: "llama-model-q8",
    });
    await expect(autoItem.locator(".badge", { hasText: "Auto" })).toBeVisible();
    // the base build is identifiable too
    await expect(
      menu
        .locator(".action-menu__item", { hasText: "llama-model" })
        .first()
        .locator(".badge", { hasText: "Base build" }),
    ).toBeVisible();
  });

  test("a variant with no memory_bytes renders as unknown, not 0", async ({
    page,
  }) => {
    await openMenu(page);
    const mlxItem = page.locator(".action-menu__item", {
      hasText: "llama-model-mlx",
    });
    await expect(mlxItem).toContainText("Unknown size");
    await expect(mlxItem).not.toContainText("0 B");
  });

  test("a variant that does not fit is still selectable", async ({ page }) => {
    await openMenu(page);
    const f16 = page.locator(".action-menu__item", {
      hasText: "llama-model-f16",
    });
    await expect(f16.locator(".badge", { hasText: "Does not fit" })).toBeVisible();
    await expect(f16).toBeEnabled();
  });

  test("choosing a specific variant sends ?variant= on the install", async ({
    page,
  }) => {
    await openMenu(page);
    await page
      .locator(".action-menu__item", { hasText: "llama-model-mlx" })
      .click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-mlx");
  });

  test("the expanded detail row lists every variant", async ({ page }) => {
    await variantRow(page).click();
    const detail = page.locator('td[colspan="8"]');
    await expect(detail).toContainText("Variants");
    await expect(detail).toContainText("llama-model-q8");
    await expect(detail).toContainText("llama-model-mlx");
    await expect(detail).toContainText("llama-model-f16");
    await expect(detail).toContainText("Unknown size");
    await expect(detail).toContainText("Auto-selected");
    await expect(detail).toContainText("Base build");
    await expect(detail).toContainText("Does not fit");
    await expect(detail).toContainText("mlx");
    // Expanding is the second trigger point, so it pays for exactly one fetch.
    expect(variantUrls).toHaveLength(1);
  });

  test("the variant rows line up as columns", async ({ page }) => {
    await variantRow(page).click();
    const rows = page.locator(".variant-row");
    await expect(rows).toHaveCount(4);
    const columns = await rows.evaluateAll((els) =>
      els.map((el) => ({
        backend: el.querySelector(".variant-row__backend").getBoundingClientRect().x,
        size: el.querySelector(".variant-row__size").getBoundingClientRect().right,
      })),
    );
    // Names differ in length, so without shared tracks each row would start
    // its backend at a different x. Sub-pixel rounding is the only tolerance.
    for (const c of columns) {
      expect(Math.abs(c.backend - columns[0].backend)).toBeLessThan(1.5);
      expect(Math.abs(c.size - columns[0].size)).toBeLessThan(1.5);
    }
  });

  test("only the informative status is badged", async ({ page }) => {
    await variantRow(page).click();
    const detail = page.locator('td[colspan="8"]');
    await expect(detail.locator(".variant-row")).toHaveCount(4);
    // "Fits" was true of three rows out of four and said nothing; the row that
    // does not fit is the one worth marking.
    await expect(detail.getByText("Fits", { exact: true })).toHaveCount(0);
    const unfit = detail.locator(".variant-row--unfit");
    await expect(unfit).toHaveCount(1);
    await expect(unfit).toContainText("llama-model-f16");
    await expect(unfit.locator(".badge-warning")).toHaveText("Does not fit");
    // Auto-selected still answers "what do I get if I just hit Install".
    await expect(
      detail.locator(".variant-row", { hasText: "llama-model-q8" }),
    ).toContainText("Auto-selected");
  });

  test("clicking a variant row installs that variant", async ({ page }) => {
    await variantRow(page).click();
    await page
      .locator(".variant-row", { hasText: "llama-model-mlx" })
      .click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-mlx");
  });

  test("a variant row is reachable and actionable from the keyboard", async ({
    page,
  }) => {
    await variantRow(page).click();
    const row = page.locator(".variant-row", { hasText: "llama-model-f16" });
    await row.focus();
    // A build that does not fit stays installable: the explicit choice is an
    // override the server honours.
    await expect(row).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-f16");
  });
});

// The collapsed view is the deduplicated gallery: every entry installable in
// its own right, with nothing shown twice. Here whisper-model stands in for a
// build llama-model already offers as a variant, so it is the only row that
// drops; stablediffusion-model is nobody's variant and stays. The filter is
// server-side because the listing paginates, so these specs assert on the
// request the page actually sends, not just on the rows it renders.
const COLLAPSED_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: MOCK_MODELS_RESPONSE.models.filter((m) => m.name !== "whisper-model"),
  availableModels: 2,
  totalPages: 1,
  currentPage: 1,
};

// What a search for the hidden build gets back: the build itself, even though
// browsing would have collapsed it away behind its parent.
const SEARCH_HIT_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: MOCK_MODELS_RESPONSE.models.filter((m) => m.name === "whisper-model"),
  availableModels: 1,
  totalPages: 1,
  currentPage: 1,
};

test.describe("Models Gallery - Collapsed Listing", () => {
  let listingUrls;

  test.beforeEach(async ({ page }) => {
    listingUrls = [];

    await page.route("**/api/models*", (route) => {
      const url = new URL(route.request().url());
      // Only the gallery's own listing. Sibling routes like
      // /api/models/estimate share the prefix, and the recommended-models
      // panel queries /api/models itself with its own page size, so neither
      // must pollute the record of what the page sent, nor pick up the
      // narrowed bodies below.
      const isListing =
        url.pathname.endsWith("/api/models") &&
        url.searchParams.get("items") === "9";
      if (isListing) {
        listingUrls.push(url);
      }
      const term = (url.searchParams.get("term") || "").trim();
      const collapsed =
        url.searchParams.get("collapse_variants") === "true" && term === "";
      const tag = url.searchParams.get("tag");
      // Stands in for the server: an explicit search term bypasses the
      // collapse, so a build a parent already offers is still findable by
      // name. Anything else browsing-shaped stays collapsed.
      let body = collapsed ? COLLAPSED_RESPONSE : MOCK_MODELS_RESPONSE;
      if (isListing && term === "whisper-model") body = SEARCH_HIT_RESPONSE;
      // A usecase filter matches nothing in this fixture, so the empty state
      // stays reachable and the specs can pin down what it says.
      if (isListing && tag) body = EMPTY_FILTERED_RESPONSE;
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("there is no collapse toggle to find", async ({ page }) => {
    // The control is redundant once searching bypasses the collapse, and a
    // toggle whose only job is "let me find things" is a worse answer than
    // the search box already being able to find them.
    await expect(page.getByText("One row per model")).toHaveCount(0);
    await expect(
      page.locator("label.filter-bar-group__toggle", {
        hasText: "One row per model",
      }),
    ).toHaveCount(0);
  });

  test("browsing collapses: the parent stays, the build it offers drops", async ({
    page,
  }) => {
    // A filter that kept only the entries declaring variants would wrongly
    // drop stablediffusion-model too.
    await expect(page.locator("tr", { hasText: "llama-model" })).toBeVisible();
    await expect(page.locator("tr", { hasText: "whisper-model" })).toHaveCount(
      0,
    );
    await expect(
      page.locator("tr", { hasText: "stablediffusion-model" }),
    ).toBeVisible();

    // Asserted over every listing request, so a first paint that fetched the
    // uncollapsed listing before settling would still fail.
    expect(listingUrls.length).toBeGreaterThan(0);
    for (const url of listingUrls) {
      expect(url.searchParams.get("collapse_variants")).toBe("true");
    }
  });

  test("searching a build the collapse hides still finds it", async ({
    page,
  }) => {
    // The regression this whole change exists to prevent: typing the name of
    // an entry the gallery does hold must never answer "no models found",
    // which reads as "that model does not exist".
    await page.locator(".search-bar input").fill("whisper-model");

    await expect(
      page.locator("tr", { hasText: "whisper-model" }),
    ).toBeVisible();
    await expect(page.locator(".empty-state")).toHaveCount(0);
  });

  test("the search term is sent alongside the collapse, not instead of it", async ({
    page,
  }) => {
    // The server decides what an active search means. The page keeps asking
    // for the collapsed listing so that decision lives in one place, and so
    // clearing the box goes straight back to the browsing view.
    await page.locator(".search-bar input").fill("whisper-model");
    await expect.poll(
      () => listingUrls[listingUrls.length - 1].searchParams.get("term"),
    ).toBe("whisper-model");

    const searched = listingUrls[listingUrls.length - 1];
    expect(searched.searchParams.get("collapse_variants")).toBe("true");
  });

  test("clearing the search box returns to the collapsed listing", async ({
    page,
  }) => {
    await page.locator(".search-bar input").fill("whisper-model");
    await expect(
      page.locator("tr", { hasText: "whisper-model" }),
    ).toBeVisible();

    await page.locator(".search-bar input").fill("");

    await expect(page.locator("tr", { hasText: "whisper-model" })).toHaveCount(
      0,
    );
    await expect(page.locator("tr", { hasText: "llama-model" })).toBeVisible();
  });

  test("a stored preference from the removed toggle is inert", async ({
    page,
  }) => {
    // The key outlives the control it belonged to. A user who left the toggle
    // off gets the collapsed view like everyone else rather than a listing
    // shaped by a setting they can no longer see or change.
    await page.evaluate(() => {
      localStorage.setItem("localai-models-collapse-variants-filter", "off");
    });
    await page.reload();
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });

    await expect(page.locator("tr", { hasText: "whisper-model" })).toHaveCount(
      0,
    );
    const last = listingUrls[listingUrls.length - 1];
    expect(last.searchParams.get("collapse_variants")).toBe("true");
  });

  test("the empty state no longer blames a toggle nobody can reach", async ({
    page,
  }) => {
    await page.locator(".filter-btn", { hasText: "Chat" }).click();

    await expect(page.locator(".empty-state-title")).toHaveText(
      "No models found",
    );
    await expect(page.locator(".empty-state-text")).toHaveText(
      "No models match your current search or filters.",
    );
    // The old hint told the user to turn off a control that is gone, and it
    // is no longer even true for a search: searching sees every build.
    await expect(page.locator(".empty-state-hint")).toHaveCount(0);
  });

  test("clear filters returns to the collapsed browsing view", async ({
    page,
  }) => {
    await page.locator(".filter-btn", { hasText: "Chat" }).click();
    await expect(page.locator(".empty-state")).toBeVisible();

    await page.getByRole("button", { name: "Clear filters" }).click();

    await expect(page.locator("tr", { hasText: "llama-model" })).toBeVisible();
    await expect(page.locator("tr", { hasText: "whisper-model" })).toHaveCount(
      0,
    );
  });
});

// Gallery descriptions are third-party Markdown. They used to be dumped raw
// into the UI, so a model whose description opened with an ATX heading showed
// a literal "# Name [](url)" in the list.
const MARKDOWN_DESCRIPTION =
  "# Qwen3.6-27B\n\nChat with it at [the Qwen site](https://chat.qwen.ai) for **free**.";
const MARKDOWN_MODELS_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: [
    {
      name: "markdown-model",
      description: MARKDOWN_DESCRIPTION,
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
    {
      name: "headings-model",
      description:
        "# Top Heading\n\nBody copy.\n\n## Sub Heading\n\nMore body copy.",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
    {
      name: "no-description-model",
      description: "",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
  ],
  availableModels: 3,
  installedModels: 0,
};

test.describe("Models Gallery - Markdown descriptions", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MARKDOWN_MODELS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("table cell shows the description as clean text, not raw Markdown", async ({
    page,
  }) => {
    const row = page.locator("tr", { hasText: "markdown-model" });
    const cell = row.locator("div[title]", { hasText: "Qwen3.6-27B" });

    await expect(cell).toHaveText(
      "Qwen3.6-27B Chat with it at the Qwen site for free.",
    );
    // The syntax itself must be gone, not merely rendered somewhere.
    await expect(cell).not.toContainText("#");
    await expect(cell).not.toContainText("[](");
    await expect(cell).not.toContainText("**");
    await expect(cell).not.toContainText("https://chat.qwen.ai");
    // A block element here would blow up the row height.
    await expect(cell.locator("h1")).toHaveCount(0);
  });

  test("title tooltip carries the stripped text, not raw Markdown", async ({
    page,
  }) => {
    const row = page.locator("tr", { hasText: "markdown-model" });
    const cell = row.locator("div[title]", { hasText: "Qwen3.6-27B" });

    await expect(cell).toHaveAttribute(
      "title",
      "Qwen3.6-27B Chat with it at the Qwen site for free.",
    );
  });

  test("expanded detail row renders the description as real markup", async ({
    page,
  }) => {
    await page.locator("tr", { hasText: "markdown-model" }).click();

    const detail = page.locator('td[colspan="8"]');
    await expect(detail.locator("h1", { hasText: "Qwen3.6-27B" })).toBeVisible();
    const link = detail.locator('a[href="https://chat.qwen.ai"]');
    await expect(link).toBeVisible();
    await expect(link).toHaveText("the Qwen site");
    await expect(detail.locator("strong", { hasText: "free" })).toBeVisible();
  });

  test("a model without a description still shows the placeholder", async ({
    page,
  }) => {
    const row = page.locator("tr", { hasText: "no-description-model" });
    await expect(row).toBeVisible();
    await expect(row.locator("div[title='']")).toHaveText("—");
  });

  test("a heading in the description renders on the UI type scale", async ({
    page,
  }) => {
    await page.locator("tr", { hasText: "headings-model" }).click();
    const prose = page.locator(".detail-prose__body.markdown-body");
    await expect(prose).toBeVisible();

    const h1 = prose.locator("h1");
    await expect(h1).toHaveText("Top Heading");
    const sizes = await prose.evaluate((el) => {
      const px = (sel) =>
        parseFloat(getComputedStyle(el.querySelector(sel)).fontSize);
      return { h1: px("h1"), h2: px("h2"), p: px("p") };
    });
    // The bug: an unscoped h1 inherits the browser default 2em, which is 26px
    // inside this 13px surface and swamps the pane. The scale tops out at
    // --text-xl (1.25rem / 20px), so anything at or above that is the default
    // leaking through rather than a styled heading.
    expect(sizes.h1).toBeGreaterThan(sizes.p);
    expect(sizes.h1).toBeLessThanOrEqual(20);
    expect(sizes.h1).toBeGreaterThanOrEqual(14);
    // The inverse defect: a subheading that is indistinguishable from body
    // text. It must stay below h1 and at or above the body size.
    expect(sizes.h2).toBeLessThanOrEqual(sizes.h1);
    expect(sizes.h2).toBeGreaterThanOrEqual(sizes.p);
  });

  test("the description sits outside the label/value grid on a readable measure", async ({
    page,
  }) => {
    await page.locator("tr", { hasText: "headings-model" }).click();
    const detail = page.locator('td[colspan="8"]');
    // Description is no longer a row of the scalar table.
    await expect(detail.locator("table td", { hasText: "Description" })).toHaveCount(
      0,
    );
    await expect(detail.locator(".detail-prose__label")).toHaveText(
      "Description",
    );
    const proseWidth = await page
      .locator(".detail-prose__body")
      .evaluate((el) => el.getBoundingClientRect().width);
    const paneWidth = await detail.evaluate(
      (el) => el.getBoundingClientRect().width,
    );
    // A measure, not the full pane: the cap is a ch count, so the exact pixel
    // value moves with the font, but it must stay well inside the pane.
    expect(proseWidth).toBeLessThan(paneWidth * 0.85);
  });

  test("a model without a description renders no prose block", async ({
    page,
  }) => {
    await page.locator("tr", { hasText: "no-description-model" }).click();
    const detail = page.locator('td[colspan="8"]');
    await expect(detail).toBeVisible();
    await expect(detail.locator(".detail-prose")).toHaveCount(0);
    // The scalar rows still render, so the pane is not blank.
    await expect(detail).toContainText("Backend");
  });
});

// The filter block is three deliberate bands: query scope (search + backend
// select), the use-case chip row, and the refinements (fits-in-GPU + context).
// These assert the separation holds, because the regression they guard against
// is the refinements being swept back into the chip row's wrap, where their
// position depends on how many chips happen to wrap at the current width.
test.describe("Models Gallery - Filter layout structure", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/backends/usecases", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(BACKEND_USECASES_MOCK),
      });
    });
    await page.route("**/api/resources", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_GPU_RESOURCES_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await expect(page.locator("th", { hasText: "Backend" })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("the chip row contains only use-case chips", async ({ page }) => {
    const chipRow = page.locator(".filter-bar");
    await expect(chipRow).toHaveCount(1);
    // Nothing but .filter-btn children: no toggle, no select, no slider.
    const childClasses = await chipRow.evaluate((el) =>
      Array.from(el.children).map((c) => c.className),
    );
    expect(childClasses.length).toBeGreaterThan(0);
    for (const cls of childClasses) {
      expect(cls).toContain("filter-btn");
    }
    await expect(chipRow.locator("input[type='range']")).toHaveCount(0);
    await expect(chipRow.locator(".filter-bar-group__toggle")).toHaveCount(0);
    await expect(chipRow.getByText("All Backends")).toHaveCount(0);
  });

  test("refinements live in their own band, outside the chip row", async ({
    page,
  }) => {
    const refine = page.getByTestId("models-filters-refine");
    await expect(refine).toBeVisible();
    await expect(refine.locator(".filter-bar")).toHaveCount(0);
    await expect(refine.getByText("Fits in GPU")).toBeVisible();
    await expect(refine.locator("#models-context-size")).toBeVisible();
    // The band is a sibling of the chip row, never a descendant.
    const nested = await page
      .locator(".filter-bar")
      .locator('[data-testid="models-filters-refine"]')
      .count();
    expect(nested).toBe(0);
  });

  test("the backend select sits in the query band above the chips", async ({
    page,
  }) => {
    const selectBtn = page.locator("button", { hasText: "All Backends" });
    await expect(selectBtn).toBeVisible();
    const inChipRow = await page
      .locator(".filter-bar")
      .locator("button", { hasText: "All Backends" })
      .count();
    expect(inChipRow).toBe(0);
    // Reads above the chips it gates.
    const selectBox = await selectBtn.boundingBox();
    const chipBox = await page.locator(".filter-bar").boundingBox();
    expect(selectBox.y).toBeLessThan(chipBox.y);
  });

  test("refinements stay grouped and on one band at a narrow width", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 900, height: 900 });
    const refine = page.getByTestId("models-filters-refine");
    await expect(refine).toBeVisible();
    const chipBox = await page.locator(".filter-bar").boundingBox();
    const refineBox = await refine.boundingBox();
    // Below the chip row, not interleaved with it.
    expect(refineBox.y).toBeGreaterThanOrEqual(chipBox.y + chipBox.height - 1);
    await expect(refine.getByText("Fits in GPU")).toBeVisible();
    await expect(refine.locator("#models-context-size")).toBeVisible();
  });

  test("chips expose pressed state and the context slider is labelled", async ({
    page,
  }) => {
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    await expect(chatBtn).toHaveAttribute("aria-pressed", "false");
    await chatBtn.click();
    await expect(chatBtn).toHaveAttribute("aria-pressed", "true");

    const slider = page.locator("#models-context-size");
    // The slider steps over an index, so the announced value must be the size.
    await expect(slider).toHaveAttribute("aria-valuetext", /^\d+K$/);
    await expect(page.locator("label[for='models-context-size']")).toBeVisible();
  });

  test("a keyboard-focused chip shows a focus ring", async ({ page }) => {
    // The global :focus-visible rule is wrapped in :where(), so it ties with
    // .filter-btn on specificity and loses on order. Without an explicit rule
    // the chips render their resting shadow while focused, i.e. no indicator.
    await page.locator(".filter-bar-group__search input").click();
    await page.keyboard.press("Tab"); // backend select
    await page.keyboard.press("Tab"); // first chip
    const focused = page.locator(".filter-btn:focus-visible");
    await expect(focused).toHaveCount(1);
    // The ring transitions in, so settle before reading the computed value.
    await page.waitForTimeout(400);
    const shadow = await focused.evaluate(
      (el) => getComputedStyle(el).boxShadow,
    );
    // A 3px spread ring, not the 1px/2px resting drop shadow.
    expect(shadow).toMatch(/0px 0px 0px 3px/);
  });

  test("the context control is keyboard reachable and drives the value", async ({
    page,
  }) => {
    const slider = page.locator("#models-context-size");
    const before = await slider.inputValue();
    await slider.focus();
    await expect(slider).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(slider).not.toHaveValue(before);
    await expect(slider).toHaveAttribute("aria-valuetext", /^\d+K$/);
  });
});
