<?php

use App\Models\User;
use Illuminate\Database\Seeder;

class ShopUsersTableSeeder extends Seeder
{
    /**
     * Run the database seeds.
     */
    public function run()
    {
        $users = [
            [
            	'id' => 1,
                'first_name' => 'Farid',
                'last_name' => 'Shahidi',
                'url' => 'farid-shahidi',
            	'email' => 'farid.sh69@gmail.com',
            	'password' => bcrypt('123456'),
                'activated' => 1,
            ],
            [
                'id' => 2,
                'first_name' => 'Arash',
                'last_name' => 'Masihpour',
                'url' => 'arash-masihpour',
                'email' => 'arash@gmail.com',
                'password' => bcrypt('123456'),
                'activated' => 1,
            ],
        ];
        foreach($users as $user){
            User::updateOrCreate(['id' => $user['id']], $user);
        }
    }
}