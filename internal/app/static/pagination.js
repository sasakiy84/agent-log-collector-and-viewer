"use strict";

// Shared by session lists, chronological timelines and archive selection.
class Pagination {
  constructor(element, { noun, onChange, onLatest, summary }) {
    this.element = element;
    this.noun = noun;
    this.onChange = onChange;
    this.onLatest = onLatest;
    this.summary = summary;
    this.total = 0;
    this.page = 1;
    this.pages = 1;
    this.loading = false;
    element.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-page]");
      if (!button || button.disabled || this.loading) return;
      if (button.dataset.page === "latest") this.onLatest();
      else this.go(Number(button.dataset.page));
    });
    element.addEventListener("submit", (event) => {
      event.preventDefault();
      const input = element.querySelector("input");
      if (input.checkValidity()) this.go(Number(input.value));
      else input.reportValidity();
    });
    this.update({ total: 0, offset: 0, size: 200 });
  }
  go(page) {
    if (
      this.loading ||
      !Number.isInteger(page) ||
      page < 1 ||
      page > this.pages ||
      page === this.page
    )
      return;
    this.onChange(page);
  }
  update({ total, offset, size }) {
    this.total = total;
    this.pages = Math.max(1, Math.ceil(total / size));
    this.page = Math.min(this.pages, Math.floor(offset / size) + 1);
    this.element.classList.toggle("pagination-many", this.pages >= 100);
    this.element.classList.toggle("pagination-single", this.pages === 1);
    const button = (page, text, aria, disabled = false, neighbor = false) =>
      `<button type="button" data-page="${page}" aria-label="${aria}"${neighbor ? ' class="pagination-neighbor"' : ""}${page === this.page ? ' aria-current="page"' : ""}${disabled ? " disabled" : ""}>${text}</button>`;
    // Always expose the endpoints and at most three pages around the current one.
    const numbers = [
      ...new Set([1, this.pages, this.page - 1, this.page, this.page + 1]),
    ]
      .filter((n) => n >= 1 && n <= this.pages)
      .sort((a, b) => a - b);
    let previous = 0;
    const links = numbers
      .map((n) => {
        const gap =
          n - previous > 1
            ? '<span class="pagination-gap" aria-hidden="true">…</span>'
            : "";
        previous = n;
        return (
          gap +
          button(
            n,
            n,
            `Page ${n}`,
            n === this.page || total === 0,
            n !== 1 && n !== this.pages && n !== this.page,
          )
        );
      })
      .join("");
    const id = this.element.id + "-jump";
    this.element.innerHTML = `<span class="pagination-range" role="status">${total ? `${(offset + 1).toLocaleString()}–${Math.min(offset + size, total).toLocaleString()} / ${total.toLocaleString()}` : "0"} ${this.noun}${this.summary ? this.summary() : ""}</span><div class="pagination-pages">${button(this.page - 1, "←", "Previous page", this.page === 1 || !total)}${links}${button(this.page + 1, "→", "Next page", this.page === this.pages || !total)}${this.onLatest ? button("latest", "Latest", "Show latest events", !total) : ""}</div><form class="pagination-jump"><label for="${id}">Page</label><input id="${id}" type="number" min="1" max="${this.pages}" step="1" value="${this.page}" aria-label="Page number" required${!total ? " disabled" : ""}><span>/ ${this.pages.toLocaleString()}</span><button type="submit"${!total ? " disabled" : ""}>Go</button></form>`;
    // Remember boundary/current disabling independently from request locking.
    this.element
      .querySelectorAll("button,input")
      .forEach(
        (control) => (control.dataset.unavailable = String(control.disabled)),
      );
    this.setLoading(this.loading);
  }
  setLoading(loading) {
    if (
      loading &&
      !this.loading &&
      this.element.contains(document.activeElement)
    ) {
      const focused = document.activeElement;
      this.pendingFocus =
        focused.tagName === "INPUT"
          ? "input"
          : focused.dataset.page || "submit";
    }
    this.loading = loading;
    this.element.setAttribute("aria-busy", String(loading));
    this.element
      .querySelectorAll("button,input")
      .forEach(
        (control) =>
          (control.disabled =
            loading || control.dataset.unavailable === "true"),
      );
    if (!loading && this.pendingFocus) {
      if (
        document.activeElement !== document.body &&
        !this.element.contains(document.activeElement)
      ) {
        this.pendingFocus = null;
        return;
      }
      const target =
        this.pendingFocus === "input"
          ? this.element.querySelector("input")
          : this.pendingFocus === "submit"
            ? this.element.querySelector("[type=submit]")
            : [...this.element.querySelectorAll("[data-page]")].find(
                (el) => el.dataset.page === this.pendingFocus && !el.disabled,
              );
      // A page button can become the current/disabled endpoint after navigation.
      // Focus the page field so keyboard users can immediately continue jumping.
      const fallback = this.element.querySelector("input:not(:disabled)");
      (target || fallback || this.element).focus({ preventScroll: true });
      this.pendingFocus = null;
    }
  }
}
