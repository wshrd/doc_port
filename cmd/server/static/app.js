(() => {
  const STORAGE_THEME = "docport.theme";
  const STORAGE_LANG = "docport.lang";
  const STORAGE_SEARCH = "docport.search";
  const REFRESH_MS = 5000;

  const i18n = {
    en: {
      search: "Search",
      search_placeholder: "Filter stacks…",
      theme: "Theme",
      compose: "Compose stacks",
      standalone: "Standalone",
      empty_compose: "No compose stacks found.",
      empty_standalone: "No standalone containers.",
      running: "running",
      no_ports: "No published ports",
      error: "Error",
      up: "up",
      created: "created",
      ago: "ago",
      show_more: "Show",
      more: "more",
      show_less: "Show less",
    },
    ru: {
      search: "Поиск",
      search_placeholder: "Фильтр стеков…",
      theme: "Тема",
      compose: "Compose-стеки",
      standalone: "Standalone",
      empty_compose: "Compose-стеки не найдены.",
      empty_standalone: "Нет standalone-контейнеров.",
      running: "запущено",
      no_ports: "Нет проброшенных портов",
      error: "Ошибка",
      up: "up",
      created: "created",
      ago: "ago",
      show_more: "Ещё",
      more: "",
      show_less: "Свернуть",
    },
  };

  const root = document.documentElement;
  const searchInput = document.getElementById("search");
  const langBtn = document.getElementById("lang-toggle");
  const themeBtn = document.getElementById("theme-toggle");
  const cardsRoot = document.getElementById("cards-root");
  const expandedStacks = new Set();

  function currentLang() {
    return root.getAttribute("data-lang") === "ru" ? "ru" : "en";
  }

  function applyLang(lang) {
    const pack = i18n[lang] || i18n.en;
    root.setAttribute("data-lang", lang);
    langBtn.textContent = lang.toUpperCase();
    document.querySelectorAll("[data-i18n]").forEach((el) => {
      const key = el.getAttribute("data-i18n");
      if (pack[key] !== undefined) el.textContent = pack[key];
    });
    document.querySelectorAll("[data-i18n-placeholder]").forEach((el) => {
      const key = el.getAttribute("data-i18n-placeholder");
      if (pack[key]) el.setAttribute("placeholder", pack[key]);
    });
    document.querySelectorAll("[data-i18n-title]").forEach((el) => {
      const key = el.getAttribute("data-i18n-title");
      if (pack[key]) el.setAttribute("title", pack[key]);
    });
    localStorage.setItem(STORAGE_LANG, lang);
  }

  function applyTheme(theme) {
    root.setAttribute("data-theme", theme);
    localStorage.setItem(STORAGE_THEME, theme);
  }

  function filterCards() {
    const q = (searchInput.value || "").trim().toLowerCase();
    localStorage.setItem(STORAGE_SEARCH, searchInput.value || "");
    document.querySelectorAll(".card").forEach((card) => {
      const hay = [
        card.getAttribute("data-stack") || "",
        ...Array.from(card.querySelectorAll(".c-name, .image, .port")).map((n) => n.textContent || ""),
      ]
        .join(" ")
        .toLowerCase();
      card.classList.toggle("is-hidden", q !== "" && !hay.includes(q));
    });
  }

  function rewritePortHosts(scope) {
    const host = window.location.hostname || "localhost";
    scope.querySelectorAll(".port-row a.link").forEach((a) => {
      try {
        const u = new URL(a.href);
        u.hostname = host;
        a.href = u.toString();
      } catch {
        /* ignore */
      }
    });
  }

  function bindMoreButtons(scope) {
    scope.querySelectorAll(".card").forEach((card) => {
      const name = card.getAttribute("data-stack") || "";
      if (expandedStacks.has(name)) {
        card.classList.add("is-expanded");
      }
      const btn = card.querySelector(".more-btn");
      if (!btn || btn.dataset.bound === "1") return;
      btn.dataset.bound = "1";
      btn.addEventListener("click", () => {
        card.classList.toggle("is-expanded");
        if (card.classList.contains("is-expanded")) {
          expandedStacks.add(name);
        } else {
          expandedStacks.delete(name);
        }
      });
    });
  }

  async function refreshCards() {
    try {
      const res = await fetch("/cards", { headers: { Accept: "text/html" }, cache: "no-store" });
      if (!res.ok) return;
      const html = await res.text();
      cardsRoot.innerHTML = html;
      applyLang(currentLang());
      filterCards();
      rewritePortHosts(cardsRoot);
      bindMoreButtons(cardsRoot);
    } catch {
      /* keep last good render */
    }
  }

  const savedTheme = localStorage.getItem(STORAGE_THEME);
  applyTheme(savedTheme === "light" || savedTheme === "dark" ? savedTheme : "dark");
  const savedLang = localStorage.getItem(STORAGE_LANG);
  applyLang(savedLang === "ru" || savedLang === "en" ? savedLang : "en");
  searchInput.value = localStorage.getItem(STORAGE_SEARCH) || "";
  filterCards();
  rewritePortHosts(document);
  bindMoreButtons(document);

  langBtn.addEventListener("click", () => applyLang(currentLang() === "en" ? "ru" : "en"));
  themeBtn.addEventListener("click", () => {
    applyTheme(root.getAttribute("data-theme") === "dark" ? "light" : "dark");
  });
  searchInput.addEventListener("input", filterCards);

  setInterval(refreshCards, REFRESH_MS);
})();
