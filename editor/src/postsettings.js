/* ------------------------------------------------------------------ *
 * Post settings — the edit bar's gear on blog/news post pages: title,
 * summary, meta description, date, byline, and thumbnail, saved through
 * PUT /api/posts.
 *
 * The date, the byline, and the images are properties of the post and go
 * live the moment they are saved (they order, sign, and illustrate it in
 * listings). The title, summary, and meta description are the backing
 * page's metadata: per-locale, and staged like the rest of the page's
 * content, so they reach the site with the next Publish. They are read
 * unresolved — see pagemeta.js — so an untranslated post shows the
 * default language as a placeholder rather than prefilling a copy of it.
 * ------------------------------------------------------------------ */

import { cfg, postInfo, mediaEnabled } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";
import { fetchMeta, metaFields, metaNote, localeSuffix, afterMetaSave } from "./pagemeta.js";

export function initPostSettings() {
    $("post-settings").addEventListener("click", function () {
        if (!postInfo) return;
        setMsg("Loading post settings…");
        fetchMeta().then(function (meta) {
            setMsg("");
            var fields = metaFields(meta, {
                descLabel: "Summary",
                descHint: "Shown on listing cards and in the RSS feed",
                // A post is the one page whose summary and search
                // description are different jobs, so it gets both.
                meta: true,
            });
            fields.push({ id: "date", label: "Date", type: "datetime", span: true,
                value: postInfo.publishedAt });
            // The byline, named where there is a name to show — turning
            // it off is a display choice, so the post stays recorded
            // against its author either way, and the date stays.
            fields.push({ id: "byline", type: "check", span: true,
                value: !postInfo.hideAuthor,
                label: postInfo.authorName
                    ? "Show the author's name (" + postInfo.authorName + ")"
                    : "Show the author's name" });
            // The banner above a post is a section in the template's
            // header region, edited on the page like any other; the only
            // image that belongs to the post itself is its listing
            // thumbnail.
            if (mediaEnabled) {
                fields.push({ id: "thumb", label: "Thumbnail", type: "image", span: true,
                    value: postInfo.thumbnailUrl, mediaId: postInfo.thumbnailMediaId });
            }
            fields.push(metaNote("the text"));
            return openDialog({
                message: "Post settings" + localeSuffix(),
                okLabel: "Save",
                wide: true,
                fields: fields,
            });
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
            setMsg("Saving…");
            // The locale rides in the URL: title and summary are written
            // to the metadata row of the language being edited.
            return api("/posts/" + postInfo.id + "?locale=" + encodeURIComponent(cfg.locale), {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    title: values.title,
                    summary: values.description,
                    meta_description: values.metaDescription,
                    published_at: values.date,
                    hide_author: !values.byline,
                    thumbnail_media_id: thumb.id,
                    thumbnail_url: thumb.id ? "" : thumb.url,
                }),
            }).then(function () {
                postInfo.publishedAt = values.date;
                postInfo.summary = values.description;
                postInfo.hideAuthor = !values.byline;
                postInfo.thumbnailUrl = thumb.url;
                postInfo.thumbnailMediaId = thumb.id;
                afterMetaSave("Post settings", "the text");
            });
        }).catch(function (err) { setMsg(err.message); });
    });
}
