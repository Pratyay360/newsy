(() => {

    const NEWSY_API_BASE = "https://newsy-ruby.vercel.app";
    const NEWSY_INSTALL_URL = "https://github.com/apps/newsletter-newsy/installations/new";

    const form = document.getElementById("repo-form");
    const repositoryInput = document.getElementById("repository");
    const submitButton = document.getElementById("submit-button");

    const statusFlash = document.getElementById("status-flash");
    const status = document.getElementById("status");

    const resultContainer = document.getElementById("result");
    const issueInfo = document.getElementById("issue-info");
    const subscribeIframe = document.getElementById("subscribe-iframe");

    const buttonCode = document.getElementById("button-code");
    const copyButton = document.getElementById("copy-button");

    const customBtnTextInput = document.getElementById("custom-btn-text");
    const btnStyleSelect = document.getElementById("btn-style");
    const btnSizeSelect = document.getElementById("btn-size");

    let currentActiveIssue = null;
    let currentRepository = null;

    const BUTTON_STYLES = {
        "primer-primary": {
            background: "#0969da",
            color: "#ffffff",
            border: "1px solid rgba(27, 31, 36, 0.15)",
            hoverBackground: "#0860c4",
        },
        "primer-green": {
            background: "#1f883d",
            color: "#ffffff",
            border: "1px solid rgba(27, 31, 36, 0.15)",
            hoverBackground: "#1a7f37",
        },
        "primer-dark": {
            background: "#21262d",
            color: "#f0f6fc",
            border: "1px solid #8b949e",
            hoverBackground: "#30363d",
        },
        "purple-glow": {
            background: "#7c3aed",
            color: "#ffffff",
            border: "1px solid #a78bfa",
            hoverBackground: "#6d28d9",
            shadow: "0 0 14px rgba(139, 92, 246, 0.65)",
        },
        minimal: {
            background: "transparent",
            color: "#0969da",
            border: "1px solid #0969da",
            hoverBackground: "#ddf4ff",
        },
    };

    const BUTTON_SIZES = {
        sm: {
            padding: "5px 12px",
            fontSize: "12px",
            borderRadius: "6px",
        },
        md: {
            padding: "7px 16px",
            fontSize: "14px",
            borderRadius: "6px",
        },
        lg: {
            padding: "10px 20px",
            fontSize: "16px",
            borderRadius: "8px",
        },
    };

    init();

    function init() {
        form.addEventListener("submit", handleSubmit);

        [customBtnTextInput, btnStyleSelect, btnSizeSelect].forEach((element) => {
            element.addEventListener("input", updateSnippet);
            element.addEventListener("change", updateSnippet);
        });

        copyButton.addEventListener("click", copyToClipboard);
    }

    async function handleSubmit(event) {
        event.preventDefault();

        const parsedRepository = parseRepository(repositoryInput.value);

        if (!parsedRepository) {
            showStatus(
                "Enter a valid GitHub repository, such as owner/repository.",
                "error",
            );
            repositoryInput.focus();
            return;
        }

        const { owner, repo } = parsedRepository;

        currentRepository = parsedRepository;
        currentActiveIssue = null;
        resultContainer.hidden = true;

        setLoading(true);
        showStatus(
            `Checking whether the Newsy app can access ${owner}/${repo}…`,
            "warn",
        );

        const access = await checkBotAccess(owner, repo);
        if (!access) {
            showStatus(
                "Could not reach the Newsy server. Set it with ?api=https://your-server and try again.",
                "error",
            );
            setLoading(false);
            return;
        }
        if (!access.installed) {
            showStatus(
                `The Newsy app is not installed on ${owner}/${repo}. Install it first: ${NEWSY_INSTALL_URL}`,
                "error",
            );
            setLoading(false);
            return;
        }

        showStatus(
            "Searching GitHub for an open issue labeled “newsletter”…",
            "warn",
        );

        try {
            const issue = await fetchGitHubIssue(owner, repo, "newsletter");

            if (issue) {
                currentActiveIssue = issue;
                hideStatus();
            } else {
                currentActiveIssue = createMockIssue(owner, repo);
                showStatus(
                    `No open issue labeled “newsletter” was found in ${owner}/${repo}. Showing a preview instead.`,
                    "warn",
                );
            }

            renderResult(currentActiveIssue);
        } catch (error) {
            console.warn("GitHub API request failed:", error);

            currentActiveIssue = createMockIssue(owner, repo);

            showStatus(
                "GitHub could not be reached or its API limit was exceeded. Showing a preview instead.",
                "warn",
            );

            renderResult(currentActiveIssue);
        } finally {
            setLoading(false);
        }
    }

    function parseRepository(input) {
        const value = input.trim().replace(/\/+$/, "");

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

    async function fetchGitHubIssue(owner, repo, label) {
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 8000);

        try {
            const endpoint =
                `https://api.github.com/repos/` +
                `${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/issues` +
                `?state=open&labels=${encodeURIComponent(label)}&per_page=100`;

            const response = await fetch(endpoint, {
                method: "GET",
                headers: {
                    Accept: "application/vnd.github+json",
                },
                signal: controller.signal,
            });

            if (response.status === 403 || response.status === 429) {
                throw new Error("GitHub API rate limit exceeded");
            }

            if (response.status === 404) {
                throw new Error("Repository not found");
            }

            if (!response.ok) {
                throw new Error(`GitHub API returned ${response.status}`);
            }

            const issues = await response.json();

            const issue = issues.find(
                (candidate) =>
                    !candidate.pull_request &&
                    Number.isInteger(candidate.number) &&
                    typeof candidate.title === "string" &&
                    isSafeGitHubUrl(candidate.html_url),
            );

            return issue ? normalizeIssue(issue) : null;
        } finally {
            clearTimeout(timeout);
        }
    }

    function normalizeIssue(issue) {
        return {
            number: issue.number,
            title: issue.title,
            html_url: issue.html_url,
        };
    }

    function createMockIssue(owner, repo) {
        return {
            number: 42,
            title: `[NEWSLETTER] Welcome to ${repo} updates`,
            html_url: `https://github.com/${owner}/${repo}/issues/42`,
            mock: true,
        };
    }

    function renderResult(issue) {
        resultContainer.hidden = false;

        issueInfo.textContent = `#${issue.number}: ${issue.title}`;

        updateIframe(issue);
        updateSnippet();

        resultContainer.scrollIntoView({
            behavior: "smooth",
            block: "nearest",
        });
    }

    function updateIframe(issue) {
        const button = buildButtonMarkup(issue.html_url);
        const title = escapeHTML(issue.title);
        const repository = escapeHTML(
            `${currentRepository.owner}/${currentRepository.repo}`,
        );

        subscribeIframe.srcdoc = `
        <!doctype html>
        <html lang="en">
          <head>
            <meta charset="utf-8">
            <meta name="viewport" content="width=device-width, initial-scale=1">
            <title>GitHub subscription</title>
            <style>
              :root {
                color-scheme: light dark;
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI",
                  sans-serif;
              }
  
              body {
                margin: 0;
                padding: 16px;
                background: transparent;
                color: #24292f;
              }
  
              @media (prefers-color-scheme: dark) {
                body {
                  color: #f0f6fc;
                }
              }
  
              .subscription {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 16px;
                padding: 14px;
                border: 1px solid #d0d7de;
                border-radius: 8px;
                background: rgba(255, 255, 255, 0.04);
              }
  
              .details {
                min-width: 0;
              }
  
              .title {
                margin: 0;
                overflow: hidden;
                font-size: 14px;
                font-weight: 600;
                text-overflow: ellipsis;
                white-space: nowrap;
              }
  
              .repository {
                margin: 5px 0 0;
                color: #656d76;
                font-size: 12px;
              }
  
              @media (max-width: 460px) {
                .subscription {
                  align-items: flex-start;
                  flex-direction: column;
                }
              }
            </style>
          </head>
          <body>
            <div class="subscription">
              <div class="details">
                <p class="title">${title}</p>
                <p class="repository">${repository} · Issue #${issue.number}</p>
              </div>
              ${button}
            </div>
          </body>
        </html>
      `;
    }

    function buildButtonMarkup(url) {
        const buttonText =
            customBtnTextInput.value.trim() || "Subscribe on GitHub";

        const style =
            BUTTON_STYLES[btnStyleSelect.value] || BUTTON_STYLES["primer-green"];

        const size = BUTTON_SIZES[btnSizeSelect.value] || BUTTON_SIZES.md;

        const inlineStyle = [
            "display:inline-block",
            "font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif",
            "font-weight:600",
            "line-height:1.25",
            "text-align:center",
            "text-decoration:none",
            "cursor:pointer",
            `padding:${size.padding}`,
            `font-size:${size.fontSize}`,
            `border-radius:${size.borderRadius}`,
            `background:${style.background}`,
            `color:${style.color}`,
            `border:${style.border}`,
            `box-shadow:${style.shadow || "none"}`,
            "transition:background .15s ease, transform .15s ease",
        ].join(";");

        return `
        <a
          href="${escapeHTML(url)}"
          target="_blank"
          rel="noopener noreferrer"
          style="${escapeHTML(inlineStyle)}"
        >${escapeHTML(buttonText)}</a>
      `;
    }

    function updateSnippet() {
        if (!currentActiveIssue) {
            return;
        }

        buttonCode.value = buildButtonMarkup(currentActiveIssue.html_url)
            .replace(/\s+/g, " ")
            .replace(/> /g, ">")
            .replace(/ </g, "<")
            .trim();
    }

    async function copyToClipboard() {
        if (!buttonCode.value) {
            return;
        }

        try {
            await navigator.clipboard.writeText(buttonCode.value);
        } catch {
            buttonCode.focus();
            buttonCode.select();
            navigator.clipboard("copy");
            buttonCode.setSelectionRange(0, 0);
        }

        const originalText = copyButton.textContent;
        copyButton.textContent = "Copied!";
        copyButton.classList.remove("btn-secondary");
        copyButton.classList.add("btn-primary");

        setTimeout(() => {
            copyButton.textContent = originalText;
            copyButton.classList.remove("btn-primary");
            copyButton.classList.add("btn-secondary");
        }, 2000);
    }

    function setLoading(isLoading) {
        submitButton.disabled = isLoading;
        submitButton.textContent = isLoading
            ? "Loading…"
            : "Generate iframe";
    }

    function showStatus(message, type = "warn") {
        status.textContent = message;
        statusFlash.hidden = false;

        const className =
            type === "error"
                ? "flash flash-error"
                : type === "success"
                    ? "flash flash-success"
                    : "flash flash-warn";

        status.className = className;
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
