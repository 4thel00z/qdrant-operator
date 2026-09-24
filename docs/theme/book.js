// Turns the book title into a wordmark linking back to the book's front page.
document.addEventListener("DOMContentLoaded", function () {
  var title = document.querySelector(".menu-title");
  if (!title) {
    return;
  }
  var root = typeof path_to_root === "string" ? path_to_root : "";
  title.textContent = "";
  var link = document.createElement("a");
  link.href = root + "index.html";
  link.appendChild(document.createTextNode("Qdrant Operator"));
  title.appendChild(link);
  var section = document.createElement("span");
  section.className = "wordmark-section";
  section.textContent = "Docs";
  title.appendChild(section);
});
