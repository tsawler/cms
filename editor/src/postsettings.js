/* ------------------------------------------------------------------ *
 * Post settings — the edit bar's gear on blog/news post pages: date,
 * summary, thumbnail, and header image, saved through PUT /api/posts.
 * These fields have no draft state (they order and describe the post in
 * listings), so a save is live at once; the body of the post still goes
 * through the normal draft/publish flow.
 * ------------------------------------------------------------------ */

import { postInfo, mediaEnabled } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";

export function initPostSettings() {
    $("post-settings").addEventListener("click", function () {
        if (!postInfo) return;
        var fields = [
            { id: "date", label: "Date", type: "datetime", value: postInfo.publishedAt },
            { id: "summary", label: "Summary", type: "text", value: postInfo.summary,
                placeholder: "Shown in listings and feeds" },
        ];
        if (mediaEnabled) {
            fields.push(
                { id: "thumb", label: "Thumbnail", type: "image", value: postInfo.thumbnailUrl },
                { id: "header", label: "Header image", type: "image", value: postInfo.headerUrl });
        }
        openDialog({
            message: "Post settings",
            okLabel: "Save",
            fields: fields,
        }).then(function (values) {
            if (!values) return;
            // Image fields are absent when media is disabled; keep the
            // stored URLs rather than wiping them.
            var thumb = values.thumb !== undefined ? values.thumb : (postInfo.thumbnailUrl || "");
            var header = values.header !== undefined ? values.header : (postInfo.headerUrl || "");
            api("/posts/" + postInfo.id, {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    summary: values.summary,
                    published_at: values.date,
                    thumbnail_url: thumb,
                    header_url: header,
                }),
            }).then(function () {
                postInfo.publishedAt = values.date;
                postInfo.summary = values.summary;
                postInfo.thumbnailUrl = thumb;
                postInfo.headerUrl = header;
                flash("Post settings saved — the page shows them after a reload.");
            }).catch(function (err) { setMsg(err.message); });
        });
    });
}
