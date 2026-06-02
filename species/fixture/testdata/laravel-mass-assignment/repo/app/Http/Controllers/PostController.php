<?php

namespace App\Http\Controllers;

use App\Models\Post;

// PostController is a hermetic fixture for laravel-mass-assignment. store() mass-
// assigns the ENTIRE request — Post::create($request->all()) — the smell: a client
// can POST fields the form never exposed (e.g. is_published, author_id) and write
// them straight to the model. show() in this fixture reads a Collection's ->all()
// and a request()->validated() call: NEITHER is request-input mass-assignment, so
// the detector's request-receiver constraint leaves them alone — proving the
// "unvalidated $request->all() only" discrimination.
class PostController
{
    public function store($request)
    {
        return Post::create($request->all());
    }

    public function index($request, $collection)
    {
        // non-matching: $collection->all() is a Collection read, not request input.
        $items = Post::create($collection->all());

        // non-matching: validated() is the safe, whitelisted subset.
        $safe = Post::create($request->validated());

        return [$items, $safe];
    }
}
