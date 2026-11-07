document.body.addEventListener("htmx:configRequest", (event) => {
  const token = document.querySelector('meta[name="csrf-token"]')?.content;
  if (token) event.detail.headers["X-CSRF-Token"] = token;
});

document.body.addEventListener("htmx:responseError", (event) => {
  const messages = {
    401: "Please log in",
    403: "You are not allowed to do that",
    404: "Not found",
  };
  const toast = document.getElementById("toast");
  if (toast) toast.textContent = messages[event.detail.xhr.status] || "Something went wrong";
});

document.body.addEventListener("movieDeleted", () => {
  const toast = document.getElementById("toast");
  if (toast) toast.textContent = "Movie deleted.";
});

document.body.addEventListener("htmx:afterSwap", (event) => {
  if (event.detail.target?.id !== "movie-list") return;

  const pager = document.getElementById("pager");
  const hint = document.getElementById("pager-hint");
  if (pager) pager.hidden = true;
  if (hint) hint.hidden = false;
});

const chat = document.getElementById("chat");
if (chat) {
  const list = document.getElementById("chat-messages");
  const form = document.getElementById("chat-form");
  const input = document.getElementById("chat-text");
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  // The HttpOnly session cookie authenticates the handshake.
  const socket = new WebSocket(`${scheme}://${location.host}/ws/movies/${chat.dataset.movieId}`);

  const show = (text) => {
    const item = document.createElement("li");
    item.textContent = text;
    list.append(item);
    list.scrollTop = list.scrollHeight;
  };

  // Messages appear only when the server echoes them, so the sender sees no duplicates.
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.error) {
      show(`Error: ${message.error}`);
      return;
    }
    const time = new Date(message.time).toLocaleTimeString();
    show(`[${time}] ${message.author}: ${message.text}`);
  });
  socket.addEventListener("close", () => show("Disconnected. Reload the page to reconnect."));

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text || socket.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ text }));
    input.value = "";
  });
}
