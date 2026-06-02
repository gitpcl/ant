<?php

namespace App\Services;

use App\Models\User;

// Report is a hermetic fixture for laravel-n+1-eager-load. build() iterates a
// User collection and touches the posts RELATION per row ($user->posts) without
// eager-loading it — the N+1 smell. totals() below iterates a plain int array and
// touches NO relation on the loop variable, so the detector must leave it alone —
// proving the rule discriminates relation-access loops from plain aggregation.
class Report
{
    public function build(): array
    {
        $users = User::all();
        $rows = [];
        foreach ($users as $user) {
            $rows[] = $user->posts->count();
        }

        return $rows;
    }

    public function totals(array $nums): int
    {
        $sum = 0;
        foreach ($nums as $n) {
            $sum += $n;
        }

        return $sum;
    }
}
