(() => {
  "use strict";

  function dismiss(flash) {
    const hadFocus = flash.contains(document.activeElement);
    flash.remove();
    if (hadFocus) {
      document.getElementById("main")?.focus();
    }
  }

  for (const button of document.querySelectorAll("[data-reveal]")) {
    const input = document.getElementById(button.dataset.reveal);
    if (!input) continue;
    button.hidden = false;
    button.addEventListener("click", () => {
      const reveal = input.type === "password";
      input.type = reveal ? "text" : "password";
      button.setAttribute("aria-pressed", String(reveal));
    });
  }

  const dropdowns = document.querySelectorAll("details[data-dropdown]");
  document.addEventListener("click", (event) => {
    for (const dropdown of dropdowns) {
      if (dropdown.open && !dropdown.contains(event.target)) dropdown.open = false;
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    for (const dropdown of dropdowns) {
      if (!dropdown.open) continue;
      dropdown.open = false;
      dropdown.querySelector("summary").focus();
    }
  });

  for (const flash of document.querySelectorAll("[data-flash]")) {
    const close = flash.querySelector(".flash__close");
    const timer = flash.querySelector(".flash__timer");
    close.hidden = false;
    close.addEventListener("click", () => dismiss(flash));
    if (timer) {
      timer.addEventListener("animationend", () => dismiss(flash));
      flash.classList.add("flash--timed");
    }
  }

  // --- Table Pagination ---
  function initTablePagination() {
    const containers = document.querySelectorAll("[data-table-pagination]");
    containers.forEach((container, idx) => {
      const table = container.querySelector("table");
      if (!table) return;
      const tbody = table.querySelector("tbody");
      if (!tbody) return;
      const allRows = Array.from(tbody.querySelectorAll("tr"));
      if (allRows.length === 0) return;

      // Unique ID for label accessibility
      const tableId = container.id || table.id || `table-paginated-${idx + 1}`;
      const defaultPageSize = parseInt(container.dataset.tablePagination, 10) || 10;
      let pageSize = parseInt(localStorage.getItem("leafygo_page_size"), 10) || defaultPageSize;
      let currentPage = 1;

      // Create toolbar
      const toolbar = document.createElement("div");
      toolbar.className = "table-pagination";
      toolbar.setAttribute("role", "region");
      toolbar.setAttribute("aria-label", "Table pagination controls");

      const selectId = `page-size-select-${idx + 1}`;
      toolbar.innerHTML = `
        <div class="table-pagination__left">
          <div class="table-pagination__length">
            <label for="${selectId}">Show</label>
            <select class="select select--sm table-pagination__select" id="${selectId}">
              <option value="5">5</option>
              <option value="10">10</option>
              <option value="25">25</option>
              <option value="50">50</option>
              <option value="100">100</option>
            </select>
            <span>entries</span>
          </div>
          <div class="table-pagination__info" aria-live="polite"></div>
        </div>
        <nav class="pagination-nav" aria-label="Table navigation">
          <button type="button" class="btn btn--sm pagination__prev" aria-label="Previous page">Previous</button>
          <ul class="pagination__pages" role="list"></ul>
          <button type="button" class="btn btn--sm pagination__next" aria-label="Next page">Next</button>
        </nav>
      `;

      container.parentNode.insertBefore(toolbar, container.nextSibling);

      const sizeSelect = toolbar.querySelector(".table-pagination__select");
      const info = toolbar.querySelector(".table-pagination__info");
      const pagesList = toolbar.querySelector(".pagination__pages");
      const prevBtn = toolbar.querySelector(".pagination__prev");
      const nextBtn = toolbar.querySelector(".pagination__next");

      // Set initial select value
      const validSizes = [5, 10, 25, 50, 100];
      if (!validSizes.includes(pageSize)) {
        pageSize = defaultPageSize;
      }
      sizeSelect.value = String(pageSize);

      function getVisibleRows() {
        return allRows.filter((r) => r.dataset.filtered !== "true");
      }

      function update() {
        const visibleRows = getVisibleRows();
        const total = visibleRows.length;
        const totalPages = Math.max(1, Math.ceil(total / pageSize));

        if (currentPage > totalPages) {
          currentPage = totalPages;
        }
        if (currentPage < 1) {
          currentPage = 1;
        }

        const startIndex = (currentPage - 1) * pageSize;
        const endIndex = Math.min(startIndex + pageSize, total);

        // Update rows visibility
        allRows.forEach((row) => {
          if (row.dataset.filtered === "true") {
            row.hidden = true;
          } else {
            const index = visibleRows.indexOf(row);
            row.hidden = index < startIndex || index >= endIndex;
          }
        });

        // Update info text
        if (total === 0) {
          info.textContent = "Showing 0 to 0 of 0 entries";
        } else {
          info.textContent = `Showing ${startIndex + 1} to ${endIndex} of ${total} entries${
            visibleRows.length < allRows.length ? ` (filtered from ${allRows.length})` : ""
          }`;
        }

        // Update buttons
        prevBtn.disabled = currentPage <= 1;
        nextBtn.disabled = currentPage >= totalPages;

        // Render page buttons
        pagesList.innerHTML = "";
        const pageNumbers = getPageNumbers(currentPage, totalPages);
        pageNumbers.forEach((p) => {
          const li = document.createElement("li");
          if (p === "gap") {
            li.innerHTML = `<span class="pagination__gap" aria-hidden="true">…</span>`;
          } else {
            const btn = document.createElement("button");
            btn.type = "button";
            btn.className = "pagination__page";
            btn.textContent = String(p);
            if (p === currentPage) {
              btn.setAttribute("aria-current", "page");
            }
            btn.addEventListener("click", () => {
              currentPage = p;
              update();
            });
            li.appendChild(btn);
          }
          pagesList.appendChild(li);
        });
      }

      function getPageNumbers(current, total) {
        if (total <= 7) {
          return Array.from({ length: total }, (_, i) => i + 1);
        }
        const pages = [];
        let gap = false;
        for (let i = 1; i <= total; i++) {
          if (i === 1 || i === total || (i >= current - 1 && i <= current + 1)) {
            pages.push(i);
            gap = false;
          } else if (!gap) {
            pages.push("gap");
            gap = true;
          }
        }
        return pages;
      }

      // Check for pre-selected row (e.g. ?edit=...)
      const selectedIndex = allRows.findIndex((r) => r.classList.contains("is-selected"));
      if (selectedIndex >= 0) {
        currentPage = Math.floor(selectedIndex / pageSize) + 1;
      }

      sizeSelect.addEventListener("change", () => {
        pageSize = parseInt(sizeSelect.value, 10);
        localStorage.setItem("leafygo_page_size", String(pageSize));
        currentPage = 1;
        update();
      });

      prevBtn.addEventListener("click", () => {
        if (currentPage > 1) {
          currentPage--;
          update();
        }
      });

      nextBtn.addEventListener("click", () => {
        const totalPages = Math.ceil(getVisibleRows().length / pageSize);
        if (currentPage < totalPages) {
          currentPage++;
          update();
        }
      });

      // Allow external filter integration
      container._applyTableFilter = function (filterFn) {
        allRows.forEach((row) => {
          row.dataset.filtered = filterFn(row) ? "false" : "true";
        });
        currentPage = 1;
        update();
      };

      update();
    });
  }

  // --- Combobox Component ---
  function initCombobox() {
    const comboboxes = document.querySelectorAll("[data-combobox]");
    comboboxes.forEach((combobox) => {
      const input = combobox.querySelector(".combobox__input");
      const dropdown = combobox.querySelector(".combobox__dropdown");
      const clearBtn = combobox.querySelector(".combobox__clear");
      const hiddenInput = combobox.querySelector('input[type="hidden"]');
      const filterTableId = combobox.dataset.filterTable;
      const targetTableContainer = filterTableId ? document.getElementById(filterTableId) : null;

      if (!input || !dropdown) return;

      const options = Array.from(dropdown.querySelectorAll(".combobox__option"));
      let highlightedIndex = -1;

      function openDropdown() {
        dropdown.hidden = false;
        input.setAttribute("aria-expanded", "true");
      }

      function closeDropdown() {
        dropdown.hidden = true;
        input.setAttribute("aria-expanded", "false");
        highlightedIndex = -1;
        options.forEach((opt) => opt.classList.remove("is-highlighted"));
      }

      function filterOptions(query) {
        const q = query.trim().toLowerCase();
        let visibleCount = 0;
        options.forEach((opt) => {
          const text = opt.textContent.trim().toLowerCase();
          const matches = !q || text.includes(q);
          opt.hidden = !matches;
          if (matches) visibleCount++;
        });

        let emptyMsg = dropdown.querySelector(".combobox__empty");
        if (visibleCount === 0) {
          if (!emptyMsg) {
            emptyMsg = document.createElement("li");
            emptyMsg.className = "combobox__empty";
            emptyMsg.textContent = "No matching options";
            dropdown.appendChild(emptyMsg);
          }
          emptyMsg.hidden = false;
        } else if (emptyMsg) {
          emptyMsg.hidden = true;
        }
      }

      function selectOption(option) {
        const val = option.dataset.value || option.textContent.trim();
        const text = option.textContent.trim();
        input.value = text;
        if (hiddenInput) {
          hiddenInput.value = val;
        }
        if (clearBtn) clearBtn.hidden = false;
        closeDropdown();

        if (targetTableContainer && typeof targetTableContainer._applyTableFilter === "function") {
          targetTableContainer._applyTableFilter((row) => {
            return row.textContent.toLowerCase().includes(text.toLowerCase());
          });
        }
      }

      input.addEventListener("focus", () => {
        filterOptions(input.value);
        openDropdown();
      });

      input.addEventListener("input", () => {
        const q = input.value;
        if (clearBtn) clearBtn.hidden = !q;
        filterOptions(q);
        openDropdown();

        // If this combobox filters a table, filter the table live as typed
        if (targetTableContainer && typeof targetTableContainer._applyTableFilter === "function") {
          targetTableContainer._applyTableFilter((row) => {
            if (!q.trim()) return true;
            return row.textContent.toLowerCase().includes(q.trim().toLowerCase());
          });
        }

        // If user typed exact match for add-member, sync hidden input
        if (hiddenInput) {
          const exact = options.find((opt) => opt.textContent.trim().toLowerCase() === q.trim().toLowerCase());
          hiddenInput.value = exact ? exact.dataset.value : "";
        }
      });

      input.addEventListener("keydown", (e) => {
        const visibleOpts = options.filter((opt) => !opt.hidden);
        if (e.key === "ArrowDown") {
          e.preventDefault();
          if (dropdown.hidden) {
            openDropdown();
            return;
          }
          if (visibleOpts.length === 0) return;
          highlightedIndex = (highlightedIndex + 1) % visibleOpts.length;
          options.forEach((opt) => opt.classList.remove("is-highlighted"));
          visibleOpts[highlightedIndex].classList.add("is-highlighted");
          visibleOpts[highlightedIndex].scrollIntoView({ block: "nearest" });
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          if (dropdown.hidden) {
            openDropdown();
            return;
          }
          if (visibleOpts.length === 0) return;
          highlightedIndex = (highlightedIndex - 1 + visibleOpts.length) % visibleOpts.length;
          options.forEach((opt) => opt.classList.remove("is-highlighted"));
          visibleOpts[highlightedIndex].classList.add("is-highlighted");
          visibleOpts[highlightedIndex].scrollIntoView({ block: "nearest" });
        } else if (e.key === "Enter") {
          if (!dropdown.hidden && highlightedIndex >= 0 && visibleOpts[highlightedIndex]) {
            e.preventDefault();
            selectOption(visibleOpts[highlightedIndex]);
          }
        } else if (e.key === "Escape") {
          closeDropdown();
        }
      });

      options.forEach((option) => {
        option.addEventListener("click", () => {
          selectOption(option);
        });
      });

      if (clearBtn) {
        clearBtn.addEventListener("click", () => {
          input.value = "";
          if (hiddenInput) hiddenInput.value = "";
          clearBtn.hidden = true;
          filterOptions("");
          closeDropdown();
          if (targetTableContainer && typeof targetTableContainer._applyTableFilter === "function") {
            targetTableContainer._applyTableFilter(() => true);
          }
          input.focus();
        });
      }

      document.addEventListener("click", (e) => {
        if (!combobox.contains(e.target)) {
          closeDropdown();
        }
      });
    });
  }

  initTablePagination();
  initCombobox();
})();
