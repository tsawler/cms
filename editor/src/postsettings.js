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
                { id: "thumb", label: "Thumbnail", type: "image",
                    value: postInfo.thumbnailUrl, mediaId: postInfo.thumbnailMediaId },
                { id: "header", label: "Header image", type: "image",
                    value: postInfo.headerUrl, mediaId: postInfo.headerMediaId });
        }
        openDialog({
            message: "Post settings",
            okLabel: "Save",
            fields: fields,
        }).then(function (values) {
            if (!values) return;
            // Image fields are absent when media is disabled; keep what is
            // stored rather than wiping it. Each image goes back as both a
            // library id and an address: the id is what the post stores,
            // and the address covers an image from outside the library,
            // which has no id.
            function image(field, url, id) {
                if (values[field] === undefined) return { url: url || "", id: id || 0 };
                return { url: values[field] || "", id: values[field + "_id"] || 0 };
            }
            var thumb = image("thumb", postInfo.thumbnailUrl, postInfo.thumbnailMediaId);
            var header = image("header", postInfo.headerUrl, postInfo.headerMediaId);
            api("/posts/" + postInfo.id, {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    summary: values.summary,
                    published_at: values.date,
                    thumbnail_media_id: thumb.id,
                    thumbnail_url: thumb.id ? "" : thumb.url,
                    header_media_id: header.id,
                    header_url: header.id ? "" : header.url,
                }),
            }).then(function () {
                postInfo.publishedAt = values.date;
                postInfo.summary = values.summary;
                postInfo.thumbnailUrl = thumb.url;
                postInfo.thumbnailMediaId = thumb.id;
                postInfo.headerUrl = header.url;
                postInfo.headerMediaId = header.id;
                flash("Post settings saved — the page shows them after a reload.");
            }).catch(function (err) { setMsg(err.message); });
        });
    });
}
