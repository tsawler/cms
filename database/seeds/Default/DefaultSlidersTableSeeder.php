<?php

use Illuminate\Database\Seeder;
use App\Models\Slider;

class DefaultSlidersTableSeeder extends Seeder
{
    /**
     * Run the database seeds.
     *
     * @return void
     */
    public function run()
    {
        $database_name = config('database.connections.mysql.database');
        $folder_name = substr($database_name, 9, 6);
        $image_folder_name = '/storage/photos/shares/' . $folder_name . '/';
        $sliders = [
            [
            	'id' => 1,
            	'title' => 'Slider Title 1',
            	'description' => 'Slider Description 1',
            	'image' => $image_folder_name . '7-slider-1.png',
            	'activated' => 1,
            ],
            [
            	'id' => 2,
            	'title' => 'Slider Title 2',
            	'description' => 'Slider Description 2',
            	'image' => $image_folder_name . '7-slider-2.png',
            	'activated' => 1,
            ],
            [
            	'id' => 3,
            	'title' => 'Slider Title 3',
            	'description' => 'Slider Description 3',
            	'image' => $image_folder_name . '7-slider-3.png',
            	'activated' => 1,
            ],
        ];

        foreach($sliders as $slider){
            Slider::updateOrCreate(['id' => $slider['id']], $slider);
        }
    }
}