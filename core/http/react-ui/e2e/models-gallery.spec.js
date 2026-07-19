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
});
