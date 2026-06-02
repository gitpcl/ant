<?php

namespace App\Livewire;

use Livewire\Component;

class Counter extends Component
{
    public $count = 0;

    public int $step = 1;

    protected $internal = 'hidden';

    public function increment(): void
    {
        $this->count += $this->step;
    }
}
