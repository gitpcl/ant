<?php

namespace App\Http\Controllers;

// UserController is a hermetic fixture for laravel-dd-dump-debug. show() has a
// stray dd($id) debug statement left in source — exactly the smell the species
// targets. The ray($id)->blue() chained call below is NOT a bare debug statement
// (it is a method-call chain), so the rule must leave it alone — proving the
// detector discriminates the standalone debug STATEMENT from a chained call.
class UserController
{
    public function show(int $id): string
    {
        dd($id);
        $label = $this->label($id);

        return $label;
    }

    public function trace(int $id): void
    {
        ray($id)->blue();
    }

    private function label(int $id): string
    {
        return 'user-'.$id;
    }
}
