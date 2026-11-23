const get = (id) => document.getElementById(id);
const kind = get("kind");
const from = get("from");
const to = get("to");
const movementAction = get("movement-action");
const accessForm = get("access-form");
const accountForm = get("account-form");
const movementForm = get("movement-form");
const currency = new Intl.NumberFormat("pt-BR", { style: "currency", currency: "BRL" });
const dates = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });
let creating = false;
let accounts = [];
let lastRequest;

async function api(path, body, key) {
  const init = body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
  if (key) init.headers["Idempotency-Key"] = key;
  let response;
  try {
    response = await fetch(path, init);
  } catch {
    throw new Error("A conexão falhou. Tente novamente.");
  }
  if (response.status === 204) return null;
  const result = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(result?.error || "Não foi possível concluir a operação.");
    error.status = response.status;
    throw error;
  }
  if (result === null) throw new Error("O servidor retornou uma resposta inesperada.");
  return result;
}

function notice(id, content = "", error = false) {
  const node = get(id);
  node.textContent = content;
  node.dataset.error = error;
}

function cents(input) {
  const [whole, fraction = ""] = input.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

function accessState(isCreating) {
  creating = isCreating;
  get("access-title").textContent = creating ? "Criar acesso" : "Entrar";
  get("access-action").textContent = creating ? "Criar acesso" : "Entrar";
  get("switch-access").textContent = creating ? "Voltar para entrar" : "Criar acesso";
  get("name-label").hidden = !creating;
  get("name").required = creating;
  get("password").minLength = creating ? 10 : 1;
  get("password").autocomplete = creating ? "new-password" : "current-password";
  notice("access-notice");
}

function showAccess() {
  get("dashboard").hidden = true;
  get("session").hidden = true;
  get("hero-total").hidden = true;
  get("access").hidden = false;
  notice("boot-notice");
  lastRequest = undefined;
  accounts = [];
  get("accounts").replaceChildren();
  get("movements").replaceChildren();
  get("session-name").textContent = "";
  get("total").textContent = "";
  accountForm.reset();
  movementForm.reset();
  notice("account-notice");
  notice("movement-notice");
}

function selectAccount(select, alternative) {
  const previous = select.value;
  select.replaceChildren(new Option("Selecione uma conta", ""));
  accounts.forEach((account) => select.add(new Option(account.name, String(account.ID))));
  if (accounts.some((account) => String(account.ID) === previous)) {
    select.value = previous;
  } else if (select.options.length > alternative) {
    select.selectedIndex = alternative;
  }
}

function movementControls() {
  get("from-label").hidden = kind.value === "credit";
  get("to-label").hidden = kind.value === "debit";
  from.required = !get("from-label").hidden;
  to.required = !get("to-label").hidden;
  movementAction.disabled = accounts.length < (kind.value === "transfer" ? 2 : 1);
}

function element(tag, value) {
  const node = document.createElement(tag);
  node.textContent = value;
  return node;
}

function renderOverview(data) {
  accounts = data.accounts;
  get("account-count").textContent = String(accounts.length);
  get("empty-accounts").hidden = accounts.length > 0;
  get("account-details").open = accounts.length === 0;
  get("total").textContent = currency.format(accounts.reduce((sum, a) => sum + a.balance_cents, 0) / 100);
  get("accounts").replaceChildren(...accounts.map((account) => {
    const row = document.createElement("li");
    row.append(element("strong", account.name), element("span", currency.format(account.balance_cents / 100)));
    return row;
  }));
  selectAccount(from, 1);
  selectAccount(to, kind.value === "transfer" ? 2 : 1);
  movementControls();

  const names = new Map(accounts.map((account) => [account.ID, account.name]));
  get("empty-ledger").hidden = data.movements.length > 0;
  get("table-wrap").hidden = data.movements.length === 0;
  get("movements").replaceChildren(...data.movements.map((movement) => {
    const source = movement.from_account_id;
    const destination = movement.to_account_id;
    const title = source && destination ? "Transferência" : destination ? "Entrada" : "Saída";
    const path = [source, destination].filter(Boolean).map((id) => names.get(id) || "Conta arquivada").join(" → ");
    const row = document.createElement("tr");
    row.setAttribute("role", "row");
    const label = element("td", title);
    if (movement.description) label.append(element("small", movement.description));
    row.append(label, element("td", path), element("td", dates.format(new Date(movement.created_at))),
      element("td", currency.format(movement.amount_cents / 100)));
    return row;
  }));
}

async function openDashboard() {
  const [user] = await api("/auth/users");
  const overview = await api("/auth/overview");
  get("session-name").textContent = user.name;
  get("session").hidden = false;
  get("hero-total").hidden = false;
  get("access").hidden = true;
  get("dashboard").hidden = false;
  notice("boot-notice");
  renderOverview(overview);
}

function report(id, error) {
  if (error.status === 401 && id !== "access-notice") {
    showAccess();
    notice("access-notice", error.message, true);
  } else {
    notice(id, error.message, true);
  }
}

get("switch-access").addEventListener("click", () => accessState(!creating));
accessForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = get("access-action");
  const input = { email: get("email").value.trim(), password: get("password").value };
  get("switch-access").disabled = true;
  button.disabled = true;
  notice("access-notice");
  try {
    if (creating) {
      await api("/users", { ...input, name: get("name").value.trim() });
      accessState(false);
    }
    await api("/login", input);
    accessForm.reset();
    await openDashboard();
  } catch (error) {
    report("access-notice", error);
  } finally {
    button.disabled = false;
    get("switch-access").disabled = false;
  }
});

get("logout").addEventListener("click", async () => {
  try {
    await api("/logout", {});
    showAccess();
  } catch (error) {
    notice("boot-notice", error.message, true);
  }
});

accountForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = new FormData(accountForm);
  const button = accountForm.querySelector("button");
  button.disabled = true;
  notice("account-notice");
  try {
    await api("/auth/accounts", { name: data.get("name").trim(), balance_cents: cents(data.get("balance")) });
    accountForm.reset();
    notice("account-notice", "Conta criada.");
    renderOverview(await api("/auth/overview"));
  } catch (error) {
    report("account-notice", error);
  } finally {
    button.disabled = false;
  }
});

kind.addEventListener("change", movementControls);
movementForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const path = kind.value === "transfer" ? "/auth/accounts/transfer" : "/auth/accounts/entries";
  const body = {
    from_account_id: from.required ? Number(from.value) : null,
    to_account_id: to.required ? Number(to.value) : null,
    amount_cents: cents(get("amount").value),
    description: get("description").value.trim(),
  };
  const signature = path + JSON.stringify(body);
  if (lastRequest?.signature !== signature) lastRequest = { signature, key: crypto.randomUUID() };
  const fields = get("movement-fields");
  fields.disabled = true;
  notice("movement-notice");
  try {
    await api(path, body, lastRequest.key);
    lastRequest = undefined;
    get("amount").value = "";
    get("description").value = "";
    notice("movement-notice", "Lançamento registrado.");
    renderOverview(await api("/auth/overview"));
  } catch (error) {
    report("movement-notice", error);
  } finally {
    fields.disabled = false;
  }
});

try {
  await openDashboard();
} catch (error) {
  showAccess();
  if (error.status !== 401) notice("access-notice", error.message, true);
}
