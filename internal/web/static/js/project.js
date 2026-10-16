function alertDiv(message, classes) {
    let template = document.createElement("template");
    let html = `
    <div class="alert ${classes}">
        <span class="alert-message"></span>
        <button type="button"
                class="close"
                data-dismiss="alert"
                aria-label="Close">
            <span aria-hidden="true">&times;</span>
        </button>
    </div>
    `
    template.innerHTML = html.trim();
    let div = template.content.firstChild;
    div.querySelector(".alert-message").textContent = message;
    return div;
}

function setVoteClass(question, voteState) {
    question.classList.remove("neutral", "upvoted", "downvoted");
    if (voteState === 1) {
        question.classList.add("upvoted");
    } else if (voteState === -1) {
        question.classList.add("downvoted");
    } else {
        question.classList.add("neutral");
    }
}

async function castVote(voteForm) {
    let data = {
        "question_id": voteForm["question_id"].value,
        "direction": voteForm["direction"].value,
    }
    let question = voteForm.parentElement.parentElement;

    try {
        let response = await fetch(voteForm.action, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Accept": "application/json",
            },
            credentials: "same-origin",
            body: JSON.stringify(data)
        });
        let responseJSON = await response.json();
        if (responseJSON.error) {
            question.before(alertDiv(responseJSON.error, "alert-danger"));
        } else {
            setVoteClass(question, responseJSON.vote_state);
        }
    } catch (err) {
        question.before(alertDiv("Couldn't record your vote. Please try again.", "alert-danger"));
    }
}

for (let element of document.getElementsByClassName("vote-form")) {
    element.addEventListener("submit", function(event) {
        event.preventDefault();
        castVote(element);
    });
}

// Restore this visitor's earlier votes. The server embeds them as JSON in the
// form {"vote<question id>": state}.
function loadVoteState() {
    let el = document.getElementById("vote-states");
    if (!el) {
        return;
    }
    let voteStates = JSON.parse(el.textContent || "{}");
    for (const v_id in voteStates) {
        let question = document.getElementById(`question${v_id.slice(v_id.search(/\d+$/))}`);
        if (question) {
            setVoteClass(question, voteStates[v_id]);
        }
    }
}

loadVoteState();
