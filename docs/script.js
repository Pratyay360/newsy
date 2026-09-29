"use strict";

/**
 * Newsy embed-button generator.
 *
 * Looks up the open announcement issue in a GitHub repository (using the
 * public GitHub API, no auth required) and generates an embeddable subscribe
 * button that points readers at it.
 */
(() => {
	const DEFAULTS = {
		label: "newsletter",
		buttonText: "Subscribe on GitHub",
		style: "primer-primary",
		size: "md",
	};

	const STORAGE_KEY = "newsy.prefs.v1";
	const THEME_KEY = "newsy.theme";
	const REQUEST_TIMEOUT_MS = 8000;

	const BUTTON_STYLES = {
		"primer-primary": {
			background: "#0969da",
			color: "#ffffff",
			border: "1px solid rgba(27, 31, 36, 0.15)",
			shadow: "none",
		},
		"primer-green": {
			background: "#1f883d",
			color: "#ffffff",
			border: "1px solid rgba(27, 31, 36, 0.15)",
			shadow: "none",
		},
		"primer-dark": {
			background: "#21262d",
			color: "#f0f6fc",
			border: "1px solid #8b949e",
			shadow: "none",
		},
		"purple-glow": {
			background: "#7c3aed",
			color: "#ffffff",
			border: "1px solid #a78bfa",
			shadow: "0 0 14px rgba(139, 92, 246, 0.65)",
		},
		minimal: {
			background: "transparent",
			color: "#0969da",
			border: "1px solid #0969da",
			shadow: "none",
		},
	};

	const BUTTON_SIZES = {
		sm: { padding: "5px 12px", fontSize: "12px", borderRadius: "6px" },
		md: { padding: "7px 16px", fontSize: "14px", borderRadius: "6px" },
		lg: { padding: "10px 20px", fontSize: "16px", borderRadius: "8px" },
	};

	// --- DOM references -------------------------------------------------

	const form = document.getElementById("repo-form");
	const repositoryInput = document.getElementById("repository");
	const labelInput = document.getElementById("search-label");
	const submitButton = document.getElementById("submit-button");

	const statusFlash = document.getElementById("status-flash");
	const statusEl = document.getElementById("status");

	const resultContainer = document.getElementById("result");
	const issueInfo = document.getElementById("issue-info");
	const issueMeta = document.getElementById("issue-meta");
	const resultNote = document.getElementById("result-note");
	const subscribeIframe = document.getElementById("subscribe-iframe");
	const issueNumberInput = document.getElementById("issue-number");

	const buttonCode = document.getElementById("button-code");
	const copyButton = document.getElementById("copy-button");

	const customBtnTextInput = document.getElementById("custom-btn-text");
	const btnStyleSelect = document.getElementById("btn-style");
	const btnSizeSelect = document.getElementById("btn-size");

	const themeToggle = document.getElementById("theme-toggle");

	// --- State ----------------------------------------------------------

	let currentRepository = null;
	let currentIssue = null;
	let requestToken = 0;

	/** Simple in-memory cache so repeat submits don't hit the rate limit. */
	const issueCache = new Map();

	init();

	// --- Setup ----------------------------------------------------------

	function init() {
		initTheme();
		restorePrefs();

		form.addEventListener("submit", handleSubmit);
		repositoryInput.addEventListener("input", clearResult);
		labelInput.addEventListener("input", clearResult);
		issueNumberInput.addEventListener("input", onIssueNumberChange);

		[customBtnTextInput, btnStyleSelect, btnSizeSelect].forEach((element) => {
			element.addEventListener("input", updateSnippet);
			element.addEventListener("change", updateSnippet);
			element.addEventListener("change", savePrefs);
		});

		[customBtnTextInput, repositoryInput, labelInput].forEach((element) => {
			element.addEventListener("change", savePrefs);
		});

		copyButton.addEventListener("click", copyToClipboard);
		themeToggle.addEventListener("click", toggleTheme);
	}

	// --- Preferences & theme -------------------------------------------

	function restorePrefs() {
		let prefs = {};

		try {
			prefs = JSON.parse(localStorage.getItem(STORAGE_KEY) || "{}") || {};
		} catch {
			prefs = {};
		}

		if (typeof prefs.repository === "string") {
			repositoryInput.value = prefs.repository;
		}
		if (typeof prefs.label === "string" && prefs.label.trim()) {
			labelInput.value = prefs.label;
		}
		if (typeof prefs.buttonText === "string" && prefs.buttonText.trim()) {
			customBtnTextInput.value = prefs.buttonText;
		}
		if (prefs.style && BUTTON_STYLES[prefs.style]) {
			btnStyleSelect.value = prefs.style;
		}
		if (prefs.size && BUTTON_SIZES[prefs.size]) {
			btnSizeSelect.value = prefs.size;
		}
	}

	function savePrefs() {
		const prefs = {
			repository: repositoryInput.value.trim(),
			label: labelInput.value.trim() || DEFAULTS.label,
			buttonText: customBtnTextInput.value.trim() || DEFAULTS.buttonText,
			style: btnStyleSelect.value,
			size: btnSizeSelect.value,
		};

		try {
			localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
		} catch {
			/* storage unavailable (private mode) — ignore */
		}
	}

	function initTheme() {
		let theme = null;

		try {
			theme = localStorage.getItem(THEME_KEY);
		} catch {
			theme = null;
		}

		if (theme !== "light" && theme !== "dark") {
			const prefersDark =
				window.matchMedia &&
				window.matchMedia("(prefers-color-scheme: dark)").matches;
			theme = prefersDark ? "dark" : "light";
		}

		applyTheme(theme);
	}

	function applyTheme(theme) {
		document.documentElement.setAttribute("data-color-mode", theme);
		document.documentElement.setAttribute("data-light-theme", "light");
		document.documentElement.setAttribute("data-dark-theme", "dark");
		// Mirror it so the iframe preview can match without extra plumbing.
		document.documentElement.style.colorScheme = theme;

		const icon = themeToggle.querySelector("span");
		if (icon) {
			icon.textContent = theme === "dark" ? "☀️" : "🌙";
		}

		themeToggle.setAttribute(
			"aria-label",
			theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
		);
	}

	function toggleTheme() {
		const next =
			document.documentElement.getAttribute("data-color-mode") === "dark"
				? "light"
				: "dark";

		applyTheme(next);

		try {
			localStorage.setItem(THEME_KEY, next);
		} catch {
			/* ignore */
		}
	}

	// --- Submit flow ----------------------------------------------------

	async function handleSubmit(event) {
		event.preventDefault();

		const parsed = parseRepository(repositoryInput.value);

		if (!parsed) {
			showStatus(
				"Enter a valid GitHub repository, such as owner/repository.",
				"error",
			);
			repositoryInput.focus();
			repositoryInput.select();
			return;
		}

		const label = (labelInput.value.trim() || DEFAULTS.label).replace(/^,|,$/g, "");

		currentRepository = parsed;
		currentIssue = null;
		hideResult();
		savePrefs();

		const token = ++requestToken;

		setLoading(true);
		showStatus(`Searching for an open issue labeled “${label}”…`, "warn");

		try {
			const issue = await fetchAnnouncementIssue(parsed.owner, parsed.repo, label);

			if (token !== requestToken) {
				return; // a newer request superseded this one
			}

			if (issue) {
				hideStatus();
				renderResult(issue);
			} else {
				showStatus(
					`No open issue labeled “${label}” was found in ${parsed.owner}/${parsed.repo}. ` +
					`Push a new post to create it, or enter the issue number below.`,
					"warn",
				);
				renderResult(createPlaceholderIssue(parsed));
			}
		} catch (error) {
			if (token !== requestToken) {
				return;
			}

			console.warn("GitHub request failed:", error);

			showStatus(
				error.message === "rate-limit"
					? "GitHub's API rate limit was reached. Enter the issue number below to build the snippet manually."
					: "GitHub could not be reached. Enter the issue number below to build the snippet manually.",
				"warn",
			);

			renderResult(createPlaceholderIssue(parsed));
		} finally {
			if (token === requestToken) {
				setLoading(false);
			}
		}
	}

	function parseRepository(input) {
		const value = String(input || "")
			.trim()
			.replace(/\/+$/, "");

		const match = value.match(
			/^(?:https?:\/\/github\.com\/)?([A-Za-z0-9-]+)\/([A-Za-z0-9_.-]+?)(?:\.git)?$/,
		);

		if (!match) {
			return null;
		}

		const [, owner, repo] = match;

		if (
			owner.length > 39 ||
			repo.length > 100 ||
			owner.startsWith("-") ||
			owner.endsWith("-") ||
			repo === "." ||
			repo === ".."
		) {
			return null;
		}

		return { owner, repo };
	}

	// --- GitHub API -----------------------------------------------------

	async function fetchAnnouncementIssue(owner, repo, label) {
		const cacheKey = `${owner}/${repo}#${label}`;

		if (issueCache.has(cacheKey)) {
			return issueCache.get(cacheKey);
		}

		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);

		try {
			const endpoint =
				`https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/issues` +
				`?state=open&labels=${encodeURIComponent(label)}&per_page=100`;

			let response;

			try {
				response = await fetch(endpoint, {
					headers: {
						Accept: "application/vnd.github+json",
						"X-GitHub-Api-Version": "2022-11-28",
					},
					signal: controller.signal,
				});
			} catch (error) {
				throw new Error(error.name === "AbortError" ? "timeout" : "network");
			}

			if (response.status === 403 || response.status === 429) {
				throw new Error("rate-limit");
			}
			if (response.status === 404) {
				throw new Error("not-found");
			}
			if (!response.ok) {
				throw new Error(`http-${response.status}`);
			}

			const issues = await response.json();

			const issue = pickAnnouncementIssue(issues);
			issueCache.set(cacheKey, issue);

			return issue;
		} finally {
			clearTimeout(timeout);
		}
	}

	/**
	 * Newsy creates a single open issue titled "announcement". Prefer that,
	 * but fall back to the most recent labelled issue.
	 */
	function pickAnnouncementIssue(issues) {
		if (!Array.isArray(issues)) {
			return null;
		}

		const candidates = issues.filter(
			(candidate) =>
				!candidate.pull_request &&
				Number.isInteger(candidate.number) &&
				typeof candidate.title === "string" &&
				isSafeGitHubUrl(candidate.html_url),
		);

		if (candidates.length === 0) {
			return null;
		}

		const announcement = candidates.find((candidate) =>
			/announcement/i.test(candidate.title),
		);

		return normalizeIssue(announcement || candidates[0]);
	}

	function normalizeIssue(issue) {
		return {
			number: issue.number,
			title: issue.title,
			html_url: issue.html_url,
			labels: Array.isArray(issue.labels)
				? issue.labels.map((label) => (typeof label === "string" ? label : label?.name)).filter(Boolean)
				: [],
			comments: Number.isInteger(issue.comments) ? issue.comments : null,
			createdAt: typeof issue.created_at === "string" ? issue.created_at : null,
			placeholder: false,
		};
	}

	function createPlaceholderIssue({ owner, repo }) {
		return {
			number: null,
			title: `Welcome to ${repo} updates`,
			html_url: `https://github.com/${owner}/${repo}/issues`,
			labels: [],
			comments: null,
			createdAt: null,
			placeholder: true,
		};
	}

	// --- Rendering ------------------------------------------------------

	function renderResult(issue) {
		currentIssue = issue;
		resultContainer.hidden = false;

		issueInfo.textContent = issue.number
			? `#${issue.number} · ${issue.title}`
			: issue.title;

		issueMeta.textContent = buildMetaText(issue);

		if (issue.placeholder) {
			resultNote.hidden = false;
			resultNote.textContent =
				"No matching issue was detected. Enter its number below to generate a working snippet.";
		} else {
			resultNote.hidden = true;
		}

		issueNumberInput.value = issue.number ? String(issue.number) : "";

		updateIframe();
		updateSnippet();

		resultContainer.scrollIntoView({ behavior: "smooth", block: "nearest" });
	}

	function buildMetaText(issue) {
		const parts = [];

		if (currentRepository) {
			parts.push(`${currentRepository.owner}/${currentRepository.repo}`);
		}

		if (issue.labels.length) {
			parts.push(issue.labels.map((label) => `#${label}`).join(" "));
		}

		if (issue.createdAt) {
			const date = new Date(issue.createdAt);
			if (!Number.isNaN(date.getTime())) {
				parts.push(
					`opened ${date.toLocaleDateString(undefined, {
						year: "numeric",
						month: "short",
						day: "numeric",
					})}`,
				);
			}
		}

		if (typeof issue.comments === "number") {
			parts.push(`${issue.comments} comment${issue.comments === 1 ? "" : "s"}`);
		}

		return parts.join(" · ");
	}

	function currentIssueUrl() {
		if (!currentRepository) {
			return null;
		}

		const number = Number.parseInt(issueNumberInput.value, 10);

		if (!Number.isInteger(number) || number <= 0) {
			return null;
		}

		return `https://github.com/${currentRepository.owner}/${currentRepository.repo}/issues/${number}`;
	}

	function onIssueNumberChange() {
		updateIframe();
		updateSnippet();
	}

	function updateIframe() {
		if (!currentIssue || !currentRepository) {
			return;
		}

		const url = currentIssueUrl();
		const button = buildButtonMarkup(url || "#");
		const title = escapeHTML(currentIssue.title);
		const repository = escapeHTML(
			`${currentRepository.owner}/${currentRepository.repo}`,
		);
		const numberLabel = currentIssueUrl() ? ` #${issueNumberInput.value}` : "";
		const theme = document.documentElement.getAttribute("data-color-mode") || "light";

		subscribeIframe.srcdoc = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Newsy button preview</title>
    <style>
      :root { color-scheme: ${theme}; }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        padding: 16px;
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: transparent;
        color: ${theme === "dark" ? "#f0f6fc" : "#24292f"};
      }
      .subscription {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 16px;
        padding: 14px;
        border: 1px solid ${theme === "dark" ? "#30363d" : "#d0d7de"};
        border-radius: 8px;
      }
      .details { min-width: 0; }
      .title {
        margin: 0;
        font-size: 14px;
        font-weight: 600;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .repository { margin: 5px 0 0; font-size: 12px; opacity: 0.7; }
      @media (max-width: 460px) {
        .subscription { align-items: flex-start; flex-direction: column; }
      }
    </style>
  </head>
  <body>
    <div class="subscription">
      <div class="details">
        <p class="title">${title}</p>
        <p class="repository">${repository}${numberLabel}</p>
      </div>
      ${button}
    </div>
  </body>
</html>`;
	}

	function buildButtonMarkup(url) {
		const buttonText = customBtnTextInput.value.trim() || DEFAULTS.buttonText;
		const style = BUTTON_STYLES[btnStyleSelect.value] || BUTTON_STYLES[DEFAULTS.style];
		const size = BUTTON_SIZES[btnSizeSelect.value] || BUTTON_SIZES.md;

		const inlineStyle = [
			"display:inline-block",
			"font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif",
			"font-weight:600",
			"line-height:1.25",
			"text-align:center",
			"text-decoration:none",
			"white-space:nowrap",
			`padding:${size.padding}`,
			`font-size:${size.fontSize}`,
			`border-radius:${size.borderRadius}`,
			`background:${style.background}`,
			`color:${style.color}`,
			`border:${style.border}`,
			`box-shadow:${style.shadow || "none"}`,
		].join(";");

		return `<a href="${escapeHTML(url)}" target="_blank" rel="noopener noreferrer" style="${escapeHTML(inlineStyle)}">${escapeHTML(buttonText)}</a>`;
	}

	function updateSnippet() {
		if (!currentIssue) {
			return;
		}

		const url = currentIssueUrl();

		if (!url) {
			buttonCode.value = "";
			copyButton.disabled = true;
			buttonCode.placeholder = "Enter an issue number to generate the embed code.";
			return;
		}

		buttonCode.placeholder = "";
		copyButton.disabled = false;
		buttonCode.value = buildButtonMarkup(url);
	}

	async function copyToClipboard() {
		if (!buttonCode.value) {
			return;
		}

		let copied = false;

		try {
			await navigator.clipboard.writeText(buttonCode.value);
			copied = true;
		} catch {
			buttonCode.focus();
			buttonCode.select();

			try {
				copied = document.execCommand("copy");
			} catch {
				copied = false;
			}

			buttonCode.setSelectionRange(0, 0);
		}

		const originalText = copyButton.textContent;

		copyButton.textContent = copied ? "Copied!" : "Press Ctrl/Cmd+C";

		setTimeout(() => {
			copyButton.textContent = originalText;
		}, 2000);
	}

	// --- UI helpers -----------------------------------------------------

	function clearResult() {
		hideResult();
	}

	function hideResult() {
		currentIssue = null;
		resultContainer.hidden = true;
	}

	function setLoading(isLoading) {
		submitButton.disabled = isLoading;
		submitButton.setAttribute("aria-busy", String(isLoading));
		submitButton.textContent = isLoading
			? "Searching…"
			: "Generate subscribe button";
	}

	function showStatus(message, type = "warn") {
		statusEl.textContent = message;
		statusFlash.hidden = false;

		const className =
			type === "error"
				? "flash flash-error"
				: type === "success"
					? "flash flash-success"
					: "flash flash-warn";

		statusEl.className = className;
	}

	function hideStatus() {
		statusFlash.hidden = true;
	}

	function isSafeGitHubUrl(value) {
		try {
			const url = new URL(value);

			return (
				url.protocol === "https:" &&
				url.hostname === "github.com" &&
				!url.username &&
				!url.password
			);
		} catch {
			return false;
		}
	}

	function escapeHTML(value) {
		return String(value).replace(/[&<>'"]/g, (character) => {
			const entities = {
				"&": "&amp;",
				"<": "&lt;",
				">": "&gt;",
				"'": "&#39;",
				'"': "&quot;",
			};

			return entities[character];
		});
	}
})();
